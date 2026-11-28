package agent

// The calc tool: Starlark for exact counting and tables, with the earlier
// tool results, the document tools and a few helpers at hand.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/firebase/genkit/go/ai"
	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

type CalcIn struct {
	Code string `json:"code" jsonschema:"Starlark code; assign the answer to 'result' or print() it"`
}

type CalcOut struct {
	Output string `json:"output,omitempty"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// scriptToolNames are the document tools a calc script can call, as
// Starlark functions with the same names.
func (s *Server) scriptToolNames() []string {
	return slices.Sorted(maps.Keys(s.scriptTools(context.Background())))
}

// scriptTools returns the document tools as Starlark functions bound to
// ctx. They go through callTool, so sharing checks, the text-local mode,
// the access log and the step events apply as for a call by the model.
func (s *Server) scriptTools(ctx context.Context) starlark.StringDict {
	d := starlark.StringDict{
		"list_collections": builtin(ctx, s, "list_collections", s.listCollections),
		"list_documents":   builtin(ctx, s, "list_documents", s.listDocuments),
		"search_documents": builtin(ctx, s, "search_documents", s.searchDocuments),
		"extract":          builtin(ctx, s, "extract", s.extract),
	}
	if !s.noText {
		d["read_document"] = builtin(ctx, s, "read_document", s.readDocument)
	}
	return d
}

// builtin makes a tool callable from Starlark with keyword arguments named
// like the tool's parameters. A call that fails (a document that cannot be
// read) returns {"error": ...} so a loop over many documents goes on; wrong
// arguments and a cancelled run end the script.
func builtin[In, Out any](ctx context.Context, s *Server, name string, fn toolFunc[In, Out]) *starlark.Builtin {
	call := func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) > 0 {
			return nil, fmt.Errorf("%s: pass keyword arguments, e.g. %s(document_id=1)", name, name)
		}
		in, err := decodeArgs[In](thread, kwargs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out, err := callTool(ctx, s, name, fn, in)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return starlarkValue(thread, map[string]string{"error": err.Error()})
		}
		return starlarkValue(thread, out)
	}
	return starlark.NewBuiltin(name, call)
}

// decodeArgs turns keyword arguments into a tool's input through JSON, so
// the parameter names and types are the tool's own; an unknown name is an
// error.
func decodeArgs[In any](thread *starlark.Thread, kwargs []starlark.Tuple) (In, error) {
	var in In
	dict := starlark.NewDict(len(kwargs))
	for _, kv := range kwargs {
		if err := dict.SetKey(kv[0], kv[1]); err != nil {
			return in, err
		}
	}
	enc, err := starlark.Call(thread, starjson.Module.Members["encode"], starlark.Tuple{dict}, nil)
	if err != nil {
		return in, err
	}
	dec := json.NewDecoder(strings.NewReader(string(enc.(starlark.String))))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, err
	}
	return in, nil
}

// starlarkValue converts a Go value to Starlark through JSON.
func starlarkValue(thread *starlark.Thread, v any) (starlark.Value, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return starlark.Call(thread, starjson.Module.Members["decode"], starlark.Tuple{starlark.String(b)}, nil)
}

// calc runs Starlark with a step limit and no access to files, network or
// the clock, and with the document tools as functions. Errors are returned
// as output, so the model can fix its code instead of the loop failing.
func (s *Server) calc(tc *ai.ToolContext, in CalcIn) (CalcOut, error) {
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
	outputs, state := starlark.NewList(nil), starlark.NewDict(0)
	if nb, ok := tc.Context.Value(runKey{}).(*notebook); ok {
		nb.calcMu.Lock()
		defer nb.calcMu.Unlock()
		if nb.state == nil {
			nb.state = state
		}
		outputs, state = nb.list(), nb.state
	}
	predeclared := s.scriptTools(tc.Context)
	for name, v := range scriptLib() {
		predeclared[name] = v
	}
	predeclared["outputs"] = outputs
	predeclared["state"] = state
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
