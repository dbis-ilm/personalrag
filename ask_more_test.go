package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/personalrag/internal/history"
	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/store"
	"github.com/dbis-ilm/personalrag/internal/testutil"
)

var (
	sourceHead = regexp.MustCompile(`^\[(\d+)\] Dokument: (\S+)`)
	rerankHead = regexp.MustCompile(`^--- Abschnitt (\d+) ---`)
)

// listModel stands in for the answer model: it rates passages mentioning
// "Großgerät" 9 and others 1, and answers by listing the files of relevant
// sources with their numbers, as the qa and qa_more prompts ask.
func listModel(g *genkit.Genkit) ai.Model {
	return genkit.DefineModel(g, "test/list", &ai.ModelOptions{Supports: &ai.ModelSupports{Multiturn: true, SystemRole: true}}, func(ctx context.Context, req *ai.ModelRequest, cb ai.ModelStreamCallback) (*ai.ModelResponse, error) {
		var prompt strings.Builder
		for _, m := range req.Messages {
			prompt.WriteString(m.Text())
		}
		lines := strings.Split(prompt.String(), "\n")
		var out []string
		if strings.Contains(prompt.String(), "Relevanz-Bewerter") {
			cur := ""
			flush := func(text string) {
				if cur == "" {
					return
				}
				score := 1
				if strings.Contains(text, "Großgerät") {
					score = 9
				}
				out = append(out, fmt.Sprintf("%s:%d", cur, score))
			}
			var text strings.Builder
			for _, l := range lines {
				if m := rerankHead.FindStringSubmatch(l); m != nil {
					flush(text.String())
					cur, text = m[1], strings.Builder{}
					continue
				}
				text.WriteString(l)
			}
			flush(text.String())
		} else {
			// Each source runs from its "[n] Dokument: …" line to the next.
			inPrevious := false
			for i, l := range lines {
				if strings.HasPrefix(l, "Bisherige Antwort:") {
					inPrevious = true
				}
				if strings.HasPrefix(l, "Neue Quellen:") {
					inPrevious = false
				}
				m := sourceHead.FindStringSubmatch(l)
				if m == nil || inPrevious {
					continue
				}
				var body strings.Builder
				for _, next := range lines[i+1:] {
					if sourceHead.MatchString(next) {
						break
					}
					body.WriteString(next)
				}
				if strings.Contains(body.String(), "Großgerät") {
					out = append(out, fmt.Sprintf("- %s [%s]", strings.TrimSuffix(m[2], ","), m[1]))
				}
			}
			if len(out) == 0 {
				out = []string{rag.NothingNew}
			}
		}
		text := strings.Join(out, "\n")
		if cb != nil {
			if err := cb(ctx, &ai.ModelResponseChunk{Content: []*ai.Part{ai.NewTextPart(text)}}); err != nil {
				return nil, err
			}
		}
		return &ai.ModelResponse{Message: ai.NewModelTextMessage(text)}, nil
	})
}

// TestAskMoreFindsAll: "show me all offers mentioning large equipment" over
// 30 offers, 14 of them relevant. The first answer covers the first page;
// AskMore pages on until every relevant offer is cited.
func TestAskMoreFindsAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "Berufungen")
	os.MkdirAll(root, 0o755)
	relevant := map[string]bool{}
	for i := range 30 {
		name := fmt.Sprintf("angebot-%02d.md", i)
		body := fmt.Sprintf("# Berufungsangebot %d\n\nDie Universität bietet eine W3-Professur mit Laborausstattung und Personal (Stelle %d).\n", i, i)
		if i%2 == 0 && len(relevant) < 14 {
			body = fmt.Sprintf("# Berufungsangebot %d\n\nDie Universität bietet eine W3-Professur und finanziert Großgeräte für das Labor (Großgerät Nr. %d).\n", i, i)
			relevant[name] = true
		}
		os.WriteFile(filepath.Join(root, name), []byte(body), 0o644)
	}
	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "b", Name: "Berufungen", Color: "#000", Folders: []string{root}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	g := genkit.Init(ctx, genkit.WithPromptFS(rag.PromptFS), genkit.WithPromptDir("prompts"))
	ix := indexer.New(g, st, nil, nil)
	go ix.Run(ctx)
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); ix.Progress().Active && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	eng, err := rag.New(g, st, listModel(g), nil)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.New(st.DB(), "")
	if err != nil {
		t.Fatal(err)
	}
	s := &AppService{appCtx: ctx, core: &core{st: st, eng: eng, hist: hist}, searches: map[int64]cachedSearch{}}

	res, err := s.Search("", "Zeige mir alle Berufungsangebote, in denen Großgeräte erwähnt werden.", "b", "answer")
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasMore {
		t.Fatal("first page should offer more")
	}
	waitTurn := func() history.Turn {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			s.mu.RLock()
			busy := s.extending[res.TurnID]
			s.mu.RUnlock()
			tt, err := hist.Turn(ctx, res.TurnID)
			if err == nil && tt.Done && !busy {
				return tt
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("answer did not finish")
		return history.Turn{}
	}
	if err := s.Ask(res.TurnID); err != nil {
		t.Fatal(err)
	}
	first := waitTurn()
	cited := func(answer string) int {
		n := 0
		for name := range relevant {
			if strings.Contains(answer, name) {
				n++
			}
		}
		return n
	}
	if n := cited(first.Answer); n == 0 || n == 14 {
		t.Fatalf("first answer should cover only the first page: %d of 14\nanswer: %q\nerror: %q note: %q", n, first.Answer, first.Error, first.Note)
	}

	if err := s.AskMore(res.TurnID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	final := waitTurn()
	if n := cited(final.Answer); n != 14 {
		t.Fatalf("after AskMore %d of 14 relevant offers cited:\n%s", n, final.Answer)
	}
	for name := range relevant {
		if strings.Count(final.Answer, name) != 1 {
			t.Errorf("%s cited %d times", name, strings.Count(final.Answer, name))
		}
	}
	if strings.Contains(final.Answer, "Laborausstattung") || final.Error != "" {
		t.Errorf("answer: %q, error %q", final.Answer, final.Error)
	}
	t.Logf("first answer %d, after AskMore 14 offers; note %q", cited(first.Answer), final.Note)
}
