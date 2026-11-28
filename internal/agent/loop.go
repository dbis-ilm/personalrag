package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/store"
)

// The in-app agent: the user's answer model plans and calls the document
// tools itself (Genkit's tool loop), with a sandboxed calculator for exact
// counts and tables. It reads every collection, since it works for the
// user inside the app, and never writes.

// AgentClient names the in-app agent in the access log.
const AgentClient = "PersonalRAG Agent"

// Step is one tool call of the agent, for showing progress.
type Step struct {
	Tool    string        `json:"tool"`
	Input   string        `json:"input"`
	Summary string        `json:"summary"`
	Error   string        `json:"error,omitempty"`
	Took    time.Duration `json:"took"`
}

type stepKey struct{}

// runKey holds the run's notebook: every tool result, so calc can work on
// the data without the model retyping it.
type runKey struct{}

type notebook struct {
	mu      sync.Mutex
	entries []*starlark.Dict
}

// add records a tool result as {tool, input, document, text}.
func (n *notebook) add(tool, input, document, text string) {
	d := starlark.NewDict(4)
	_ = d.SetKey(starlark.String("tool"), starlark.String(tool))
	_ = d.SetKey(starlark.String("input"), starlark.String(input))
	_ = d.SetKey(starlark.String("document"), starlark.String(document))
	_ = d.SetKey(starlark.String("text"), starlark.String(text))
	d.Freeze()
	n.mu.Lock()
	n.entries = append(n.entries, d)
	n.mu.Unlock()
}

func (n *notebook) list() *starlark.List {
	n.mu.Lock()
	defer n.mu.Unlock()
	vals := make([]starlark.Value, len(n.entries))
	for i, e := range n.entries {
		vals[i] = e
	}
	l := starlark.NewList(vals)
	l.Freeze()
	return l
}

// resultText turns a tool result into the document it is about and its
// plain text: extract results joined, a read document's text, else JSON.
func resultText(out any) (document, text string) {
	switch o := out.(type) {
	case ExtractOut:
		var parts []string
		for _, p := range o.Parts {
			if !rag.IsNothingFound(p.Result) {
				parts = append(parts, p.Result)
			}
		}
		return o.Document.Name, strings.Join(parts, "\n")
	case ReadDocumentOut:
		return o.Document.Name, o.Text
	}
	b, _ := json.Marshal(out)
	return "", string(b)
}

// WithSteps returns a context whose agent tool calls are reported to fn.
func WithSteps(ctx context.Context, fn func(Step)) context.Context {
	return context.WithValue(ctx, stepKey{}, fn)
}

// InApp returns a server for the in-app agent: it sees all collections.
func (s *Server) InApp() *Server {
	c := *s
	c.all = true
	return &c
}

// Tools registers the agent's tools on g (once per Genkit instance).
func (s *Server) Tools(g *genkit.Genkit) []ai.ToolRef {
	return []ai.ToolRef{
		defineTool(g, s, "list_collections", "Lists the document collections with their ids.", s.listCollections),
		defineTool(g, s, "list_documents", "Lists documents (id, name, format, pages, status), filterable by collection, folder, format and modification date, paged. Use it to cover every document of a collection.", s.listDocuments),
		defineTool(g, s, "read_document", "Returns the text of one document, paged by characters, with [Page n] markers; optionally only some pages.", s.readDocument),
		defineTool(g, s, "search_documents", "Hybrid search over the documents; returns the most relevant passages with document id, name and pages. Use it to find where something is, not to cover everything.", s.searchDocuments),
		defineTool(g, s, "extract", "Runs an instruction over one document with the local model and returns only the result per part, e.g. 'list the names under Anwesend, one per line'. Prefer it over reading long documents. pages limits it to part of the document, stop_after_match ends it at the first part with a result. Without them it reads the whole document: slow, and text elsewhere that merely resembles what is asked can produce false results.", s.extract),
		genkit.DefineTool(g, "calc", "Runs Starlark (a Python dialect: lists, dicts, loops, functions, sorted, len, str methods; json.decode/encode) to count, sum, sort or build tables exactly. The results of all earlier tool calls are in the list `outputs`, in call order; each is a dict with keys tool, input, document and text (for extract: the extracted lines; for read_document: the text). Work on outputs instead of retyping data. print() output and the variable `result` are returned. No files, no network.", calcTool),
	}
}

// ToolResult wraps a tool's output for the agent. A failing call (e.g. a
// page range the tool cannot read) comes back as Error instead of ending
// the whole run, so the model can correct the call.
type ToolResult[Out any] struct {
	Result *Out   `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func defineTool[In, Out any](g *genkit.Genkit, s *Server, name, desc string, fn toolFunc[In, Out]) ai.ToolRef {
	return genkit.DefineTool(g, name, desc, func(tc *ai.ToolContext, in In) (ToolResult[Out], error) {
		started := time.Now()
		out, oc, err := fn(tc.Context, in)
		step := Step{Tool: name, Input: summarizeInput(in), Summary: oc.summary, Took: time.Since(started)}
		e := store.AccessEntry{Client: AgentClient, Tool: name, Request: step.Input, Result: oc.summary, Kind: "ok", Duration: step.Took}
		switch {
		case err != nil:
			step.Error, e.Kind, e.Result = err.Error(), "error", err.Error()
		case oc.warn:
			e.Kind = "warn"
		}
		if lerr := s.st.LogAccess(context.WithoutCancel(tc.Context), e); lerr != nil {
			slog.Warn("agent: access log failed", "err", lerr)
		}
		if fn, ok := tc.Context.Value(stepKey{}).(func(Step)); ok {
			fn(step)
		}
		if nb, ok := tc.Context.Value(runKey{}).(*notebook); ok && err == nil {
			doc, text := resultText(out)
			nb.add(name, step.Input, doc, text)
		}
		if err != nil {
			if tc.Context.Err() != nil {
				return ToolResult[Out]{}, err // cancelled: stop the run
			}
			return ToolResult[Out]{Error: err.Error()}, nil
		}
		return ToolResult[Out]{Result: &out}, nil
	})
}

// ---- calc ----

type CalcIn struct {
	Code string `json:"code" jsonschema:"Starlark code; assign the answer to 'result' or print() it"`
}

type CalcOut struct {
	Output string `json:"output,omitempty"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// calcTool runs Starlark with a step limit and no access to files, network
// or the clock. Errors are returned as output, so the model can fix its
// code instead of the loop failing.
func calcTool(tc *ai.ToolContext, in CalcIn) (CalcOut, error) {
	var out strings.Builder
	thread := &starlark.Thread{
		Name:  "calc",
		Print: func(_ *starlark.Thread, msg string) { out.WriteString(msg + "\n") },
	}
	thread.SetMaxExecutionSteps(10_000_000)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-tc.Context.Done():
			thread.Cancel("cancelled")
		case <-finished:
		}
	}()
	outputs := starlark.NewList(nil)
	if nb, ok := tc.Context.Value(runKey{}).(*notebook); ok {
		outputs = nb.list()
	}
	predeclared := starlark.StringDict{"json": starjson.Module, "outputs": outputs}
	opts := &syntax.FileOptions{Set: true, While: true, TopLevelControl: true, GlobalReassign: true, Recursion: true}
	globals, err := starlark.ExecFileOptions(opts, thread, "calc.star", in.Code, predeclared)
	res := CalcOut{Output: strings.TrimSpace(out.String())}
	if err != nil {
		var ee *starlark.EvalError
		if errors.As(err, &ee) {
			res.Error = ee.Backtrace()
		} else {
			res.Error = err.Error()
		}
		return res, nil
	}
	if v, ok := globals["result"]; ok {
		res.Result = v.String()
	}
	if len(res.Result) > 20000 {
		res.Result = res.Result[:20000] + " …"
	}
	return res, nil
}

// ---- run ----

// agentSystem guides the model; it extends the MCP instructions.
const agentSystem = instructions + `
You are the assistant inside PersonalRAG and work only with the tools above. Plan the steps the task needs, then carry them out.
- For "all", "every", lists, counts and tables: cover every relevant document (list_documents, then extract or read_document per document). Do not stop after a few.
- When the same extraction runs over many documents, try it on one or two first and look at which parts the results come from. If the information sits in one place in every document, limit the remaining calls with pages or stop_after_match.
- Before counting, clean each document's result: one item per line, drop headings and empty results, and count an item at most once per document unless the task asks for occurrences.
- Count, sum and sort with the calc tool, never in your head: after collecting the data (e.g. extract per document), call calc once with code that loops over "outputs" (e.g. for o in outputs: if o["tool"] == "extract": for line in o["text"].splitlines(): ...). Normalise names (strip "- ", bullets and spaces) before counting. Report the counts calc returns.
- Name the documents (and pages, where known) your answer rests on.
- If something could not be read or found, say so.
- Answer in the language of the task. Today is %s.`

// Run lets model carry out task with the tools and returns its final answer.
// maxTurns caps the tool rounds.
func Run(ctx context.Context, g *genkit.Genkit, model ai.Model, config any, tools []ai.ToolRef, task string, maxTurns int) (string, error) {
	ctx = context.WithValue(ctx, runKey{}, &notebook{})
	resp, err := genkit.Generate(ctx, g,
		ai.WithModel(model),
		ai.WithConfig(config),
		ai.WithSystem(fmt.Sprintf(agentSystem, time.Now().Format("2006-01-02"))),
		ai.WithPrompt(task),
		ai.WithTools(tools...),
		ai.WithMaxTurns(maxTurns),
	)
	if err != nil {
		return "", err
	}
	return resp.Text(), nil
}
