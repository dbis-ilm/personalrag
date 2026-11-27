// Package rag is the query side: hybrid retrieval (vectors + FTS5, fused with
// RRF), LLM reranking and a streamed, cited answer from a local model.
package rag

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/i18n"
	"github.com/dbis-ilm/personalrag/internal/store"
)

// PromptFS holds the .prompt files; pass it to genkit.Init with
// genkit.WithPromptFS(PromptFS) and genkit.WithPromptDir("prompts").
//
//go:embed prompts/*.prompt
var PromptFS embed.FS

const (
	candidatesPerList = 20
	maxSources        = 8
	minRelevant       = 5 // rerank score (0–10) a source needs to be used
	// contextBudget caps the source text given to the answer model, in
	// characters (~6–8k tokens; the local model runs with 16k). It holds all
	// eight sources at their usual size, so a relevant source is left out
	// only when the sources are unusually long.
	contextBudget = 24000
)

// Result is one search hit as shown to the user, numbered from 1.
type Result struct {
	N   int
	Hit store.Hit
}

// Search is the outcome of the retrieval step.
type Search struct {
	Results   []Result
	VectorErr error // non-nil when only keyword search ran
}

// Answer summarises a finished answer.
type Answer struct {
	Confidence askyourdocs.Confidence // high | medium | low
	Used       []int                  // source numbers given to the model
	Relevant   int                    // sources rated relevant (Used may hold fewer: budget)
	Total      int                    // sources ranked
	Fallback   bool                   // no source was clearly relevant
}

type Engine struct {
	g         *genkit.Genkit
	st        *store.Store
	llm       ai.Model
	llmConfig any
	rerank    ai.Prompt
	qa        ai.Prompt
	condense  ai.Prompt
	extract   ai.Prompt
}

// New wires the engine. llm may be nil (search-only mode).
func New(g *genkit.Genkit, st *store.Store, llm ai.Model, llmConfig any) (*Engine, error) {
	e := &Engine{g: g, st: st, llm: llm, llmConfig: llmConfig}
	e.rerank = genkit.LookupPrompt(g, "rerank")
	e.qa = genkit.LookupPrompt(g, "qa")
	e.condense = genkit.LookupPrompt(g, "condense")
	e.extract = genkit.LookupPrompt(g, "extract")
	if e.rerank == nil || e.qa == nil || e.condense == nil || e.extract == nil {
		return nil, errors.New("prompts rerank/qa/condense/extract not loaded")
	}
	return e, nil
}

// HasModel reports whether answers can be generated.
func (e *Engine) HasModel() bool { return e.llm != nil }

// Condense rewrites a follow-up into a standalone question using earlier
// turns. Without an answer model it falls back to prefixing the previous
// question, which still keeps its terms in the search.
func (e *Engine) Condense(ctx context.Context, question string, history []askyourdocs.QATurn) string {
	if len(history) == 0 {
		return question
	}
	if e.llm == nil {
		return askyourdocs.FallbackStandaloneQuestion(question, askyourdocs.TrimHistory(history))
	}
	return askyourdocs.CondenseFollowUp(ctx, e.condense, e.llm, e.llmConfig, question, history)
}

// Search runs vector and keyword search and fuses them. A failing vector
// search (e.g. embedding model unreachable) degrades to keyword search.
func (e *Engine) Search(ctx context.Context, question, collectionID string) (Search, error) {
	var out Search
	vec, err := e.st.Retrieve(ctx, question, candidatesPerList, collectionID)
	if err != nil {
		slog.WarnContext(ctx, "vector search failed, keyword only", "err", err)
		out.VectorErr = err
	}
	kw, err := e.st.KeywordSearch(ctx, question, candidatesPerList, collectionID, store.GermanStopWords)
	if err != nil {
		return out, fmt.Errorf("Volltextsuche: %w", err)
	}

	byID := map[int64]store.Hit{}
	toDocs := func(hits []store.Hit) []*ai.Document {
		docs := make([]*ai.Document, len(hits))
		for i, h := range hits {
			if prev, ok := byID[h.ChunkID]; !ok || prev.Score < h.Score {
				byID[h.ChunkID] = h
			}
			// Key fusion on the chunk ID so identical texts in different
			// files stay separate sources.
			docs[i] = ai.DocumentFromText(strconv.FormatInt(h.ChunkID, 10), nil)
		}
		return docs
	}
	fused := askyourdocs.ReciprocalRankFusion([]askyourdocs.RankedList{
		{Docs: toDocs(vec), Weight: 1},
		{Docs: toDocs(kw), Weight: 1},
	}, 60, maxSources)

	for i, f := range fused {
		id, _ := strconv.ParseInt(f.Text, 10, 64)
		h := byID[id]
		h.Score = f.Score
		out.Results = append(out.Results, Result{N: i + 1, Hit: h})
	}
	return out, nil
}

// Answer reranks the results, picks the relevant ones and streams an answer
// that cites them by their numbers. onText receives text as it arrives.
func (e *Engine) Answer(ctx context.Context, question string, results []Result, onText func(string)) (Answer, error) {
	if e.llm == nil {
		return Answer{}, i18n.Errorf("core.noAnswerModel")
	}
	if len(results) == 0 {
		onText(i18n.T("answer.nothingFound"))
		return Answer{Confidence: askyourdocs.ConfidenceLow}, nil
	}

	texts := make([]string, len(results))
	for i, r := range results {
		texts[i] = r.Hit.Text
	}
	scores := askyourdocs.RerankScores(ctx, e.rerank, e.llm, e.llmConfig, question, texts, askyourdocs.RerankOptions{})

	sizes := make([]int, len(results))
	for i, r := range results {
		sizes[i] = len(firstNonEmpty(r.Hit.ParentText, r.Hit.Text))
	}
	used, relevant, fallback := selectSources(scores, sizes, contextBudget)
	top := 0
	for _, s := range scores {
		top = max(top, s)
	}
	conf := askyourdocs.RateConfidence(top, fallback)

	var ctxText strings.Builder
	nums := make([]int, 0, len(used))
	sort.Ints(used)
	for _, i := range used {
		r := results[i]
		nums = append(nums, r.N)
		fmt.Fprintf(&ctxText, "[%d] Dokument: %s", r.N, filepath.Base(r.Hit.Path))
		if p := Pages(r.Hit.Pages); p != "" {
			fmt.Fprintf(&ctxText, ", Seite %s", p)
		}
		if s := firstNonEmpty(r.Hit.Section, r.Hit.Breadcrumb); s != "" {
			fmt.Fprintf(&ctxText, ", Abschnitt: %s", s)
		}
		ctxText.WriteString("\n")
		ctxText.WriteString(firstNonEmpty(r.Hit.ParentText, r.Hit.Text))
		ctxText.WriteString("\n\n")
	}

	opts := askyourdocs.PromptOptions(e.llm, e.llmConfig, map[string]any{
		"context":  askyourdocs.WrapContext(ctxText.String()),
		"question": askyourdocs.SanitizeUntrusted(question),
	})
	opts = append(opts, ai.WithStreaming(func(_ context.Context, c *ai.ModelResponseChunk) error {
		if t := c.Text(); t != "" {
			onText(t)
		}
		return nil
	}))
	if _, err := e.qa.Execute(ctx, opts...); err != nil {
		return Answer{}, err
	}

	return Answer{Confidence: conf, Used: nums, Relevant: relevant, Total: len(results), Fallback: fallback}, nil
}

// selectSources picks the sources for the answer: every source rated
// relevant, best first, while their text fits budget characters (the best
// one always). Without a relevant source it falls back to the three best.
// used holds indexes into scores; relevant counts the sources rated
// relevant, used or not.
func selectSources(scores, sizes []int, budget int) (used []int, relevant int, fallback bool) {
	order := make([]int, len(scores))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
	spent := 0
	for _, i := range order {
		if scores[i] < minRelevant {
			continue
		}
		relevant++
		if len(used) > 0 && spent+sizes[i] > budget {
			continue // a later, shorter source may still fit
		}
		used = append(used, i)
		spent += sizes[i]
	}
	if len(used) == 0 {
		return order[:min(3, len(order))], 0, true
	}
	return used, relevant, false
}

// Pages normalises a chunk's page range; non-paged formats report "0" or "1".
func Pages(p string) string {
	if p == "" || p == "0" {
		return ""
	}
	return p
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// NothingFound is what the extract prompt answers for a part without a match.
const NothingFound = "(nothing found)"

// IsNothingFound also accepts the German marker of results cached before
// the marker became English.
func IsNothingFound(s string) bool {
	return s == NothingFound || s == "(nichts gefunden)"
}

// ModelName identifies the answer model, e.g. for cache keys; "" without one.
func (e *Engine) ModelName() string {
	if e.llm == nil {
		return ""
	}
	return e.llm.Name()
}

// ExtractPart applies instruction to one part of a document with the answer
// model. part labels it for the model, e.g. "Seiten 1-2, Teil 1 von 3".
func (e *Engine) ExtractPart(ctx context.Context, instruction, document, part, text string) (string, error) {
	if e.llm == nil {
		return "", errors.New("kein Antwortmodell eingerichtet")
	}
	resp, err := e.extract.Execute(ctx, askyourdocs.PromptOptions(e.llm, e.llmConfig, map[string]any{
		"instruction": askyourdocs.SanitizeUntrusted(instruction),
		"document":    askyourdocs.SanitizeUntrusted(document),
		"part":        part,
		"text":        askyourdocs.WrapUntrusted("document", text),
	})...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Text()), nil
}
