package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/firebase/genkit/go/ai"

	"github.com/dbis-ilm/personalrag/internal/rag"
)

func TestCalc(t *testing.T) {
	tc := &ai.ToolContext{Context: context.Background()}
	run := func(code string) CalcOut {
		t.Helper()
		out, err := (&Server{}).calc(tc, CalcIn{Code: code})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	out := run(`
lists = [["Anna", "Carl"], ["Anna"], ["Eva", "Anna"]]
counts = {}
for l in lists:
    for n in l:
        counts[n] = counts.get(n, 0) + 1
for n, c in sorted(counts.items(), key=lambda kv: -kv[1]):
    print(n, c)
result = counts["Anna"]
`)
	if out.Result != "3" || !strings.HasPrefix(out.Output, "Anna 3") || out.Error != "" {
		t.Fatalf("counting: %+v", out)
	}
	if out := run(`result = json.decode('{"a": [1, 2, 3]}')["a"][2]`); out.Result != "3" {
		t.Fatalf("json: %+v", out)
	}
	if out := run("result = (1 +"); out.Error == "" {
		t.Fatal("syntax error not reported")
	}
	if out := run("x = 0\nwhile True:\n    x += 1"); !strings.Contains(out.Error, "too many steps") {
		t.Fatalf("endless loop not stopped: %+v", out)
	}
	if out := run(`result = open("/etc/passwd")`); !strings.Contains(out.Error, "undefined") {
		t.Fatalf("file access should not exist: %+v", out)
	}
}

// calc sees earlier tool results as `outputs`, so counting needs no
// retyping.
func TestCalcOutputs(t *testing.T) {
	nb := &notebook{}
	nb.add("extract", `{"document_id":1}`, "protokoll-01.md", "- Anna Berger\n- Carl Dietrich")
	nb.add("extract", `{"document_id":2}`, "protokoll-02.md", "Anna Berger\nEva Fischer")
	nb.add("list_documents", "{}", "", `{"documents":[]}`)
	tc := &ai.ToolContext{Context: context.WithValue(context.Background(), runKey{}, nb)}
	out, err := (&Server{}).calc(tc, CalcIn{Code: `
counts = {}
for o in outputs:
    if o["tool"] != "extract":
        continue
    for line in o["text"].splitlines():
        name = line.strip().lstrip("- ").strip()
        if name:
            counts[name] = counts.get(name, 0) + 1
result = counts
`})
	if err != nil || out.Error != "" {
		t.Fatalf("calc: %+v, %v", out, err)
	}
	if out.Result != `{"Anna Berger": 2, "Carl Dietrich": 1, "Eva Fischer": 1}` {
		t.Fatalf("counts from outputs: %s", out.Result)
	}
	if out, _ := (&Server{}).calc(tc, CalcIn{Code: `outputs.append(1)`}); !strings.Contains(out.Error, "frozen") {
		t.Errorf("outputs should be read-only: %+v", out)
	}
}

func TestResultText(t *testing.T) {
	doc, text := resultText(ExtractOut{Document: DocumentOut{Name: "a.md"}, Parts: []ExtractPart{{Result: "x"}, {Result: rag.NothingFound}, {Result: "y"}}})
	if doc != "a.md" || text != "x\ny" {
		t.Fatalf("extract: %q %q", doc, text)
	}
	if _, text := resultText(map[string]int{"n": 1}); text != `{"n":1}` {
		t.Fatalf("json: %q", text)
	}
}

// calc scripts call the document tools as functions, so one script covers
// every document; a failing call is a value, not the end of the script.
func TestCalcScriptTools(t *testing.T) {
	f := setup(t)
	nb := &notebook{}
	var steps []Step
	ctx := context.WithValue(context.Background(), runKey{}, nb)
	ctx = WithSteps(ctx, func(s Step) { steps = append(steps, s) })
	tc := &ai.ToolContext{Context: ctx}
	server := f.srv.InApp()

	out, err := server.calc(tc, CalcIn{Code: `
counts = {}
for d in list_documents(collection_id="protokolle")["documents"]:
    r = extract(document_id=d["id"], instruction="Anwesend")
    if "error" in r:
        continue
    for part in r["parts"]:
        for name in part["result"].split(","):
            name = name.strip()
            if name and name != "(nothing found)":
                counts[name] = counts.get(name, 0) + 1
bad = extract(document_id=999999, instruction="x")
result = [counts["Anna Berg"], "error" in bad]
`})
	if err != nil || out.Error != "" {
		t.Fatalf("calc: %+v, %v", out, err)
	}
	if out.Result != "[3, True]" {
		t.Fatalf("result = %s", out.Result)
	}
	// 1 list, 3 extracts and the failing one, each a step and a notebook entry.
	if len(steps) != 5 || steps[0].Tool != "list_documents" || steps[4].Error == "" {
		t.Fatalf("steps: %+v", steps)
	}
	if n := nb.list().Len(); n != 4 {
		t.Fatalf("notebook has %d entries, want 4", n)
	}

	if out, _ := server.calc(tc, CalcIn{Code: `extract(1, "x")`}); !strings.Contains(out.Error, "keyword arguments") {
		t.Errorf("positional arguments: %+v", out)
	}
	if out, _ := server.calc(tc, CalcIn{Code: `list_documents(colection_id="x")`}); !strings.Contains(out.Error, "unknown field") {
		t.Errorf("typo in argument name: %+v", out)
	}
}

// With "document text stays on this computer" a script gets no read_document.
func TestCalcScriptToolsTextLocal(t *testing.T) {
	f := setup(t)
	tc := &ai.ToolContext{Context: context.Background()}
	out, _ := f.srv.InApp().TextLocal().calc(tc, CalcIn{Code: `read_document(document_id=1)`})
	if !strings.Contains(out.Error, "undefined") {
		t.Fatalf("read_document should not exist: %+v", out)
	}
}

// state carries values from one calc call to the next, functions included.
func TestCalcState(t *testing.T) {
	nb := &notebook{}
	tc := &ai.ToolContext{Context: context.WithValue(context.Background(), runKey{}, nb)}
	run := func(code string) CalcOut {
		t.Helper()
		out, err := (&Server{}).calc(tc, CalcIn{Code: code})
		if err != nil || out.Error != "" {
			t.Fatalf("calc: %+v, %v", out, err)
		}
		return out
	}
	run(`
def twice(x):
    return 2 * x
state["twice"] = twice
state["rows"] = [1, 2, 3]
`)
	if out := run(`result = [state["twice"](x) for x in state["rows"]]`); out.Result != "[2, 4, 6]" {
		t.Fatalf("state across calls: %+v", out)
	}
	// A value the script also held in a variable is frozen with the
	// script's globals; a new value replaces it.
	run(`
rows = [1]
state["held"] = rows
`)
	if out, _ := (&Server{}).calc(tc, CalcIn{Code: `state["held"].append(2)`}); !strings.Contains(out.Error, "frozen") {
		t.Fatalf("a value held in a variable should be read-only: %+v", out)
	}
	if out := run(`state["held"] = state["held"] + [2]
result = len(state["held"])`); out.Result != "2" {
		t.Fatalf("replacing a stored value: %+v", out)
	}
	// A fresh run has its own state.
	tc2 := &ai.ToolContext{Context: context.WithValue(context.Background(), runKey{}, &notebook{})}
	if out, _ := (&Server{}).calc(tc2, CalcIn{Code: `result = len(state)`}); out.Result != "0" {
		t.Fatalf("state leaked into another run: %+v", out)
	}
}

func TestCalcLib(t *testing.T) {
	tc := &ai.ToolContext{Context: context.Background()}
	run := func(code string) CalcOut {
		t.Helper()
		out, err := (&Server{}).calc(tc, CalcIn{Code: code})
		if err != nil || out.Error != "" {
			t.Fatalf("calc: %+v, %v", out, err)
		}
		return out
	}
	if out := run(`result = [normalize_name(x) for x in ["- Anna  Berg", "* Carl Dorn,", "2. Eva Fuchs", "  Anna Berg "]]`); out.Result != `["Anna Berg", "Carl Dorn", "Eva Fuchs", "Anna Berg"]` {
		t.Fatalf("normalize_name: %+v", out)
	}
	if out := run(`result = count_by(["b", "a", "b", "c", "a", "b"])`); out.Result != `{"b": 3, "a": 2, "c": 1}` {
		t.Fatalf("count_by: %+v", out)
	}
	if out := run(`result = count_by([{"n": "x"}, {"n": "y"}, {"n": "x"}], key=lambda r: r["n"])`); out.Result != `{"x": 2, "y": 1}` {
		t.Fatalf("count_by key: %+v", out)
	}
	if out := run(`result = table([{"Name": "Anna", "n": 3}, {"Name": "Carl|X", "n": 1}])`); out.Result != "\"| Name | n |\\n| --- | --- |\\n| Anna | 3 |\\n| Carl\\\\|X | 1 |\\n\"" {
		t.Fatalf("table markdown: %q", out.Result)
	}
	if out := run(`result = table([["Anna", 3], ["Carl, Jr.", 1]], columns=["Name", "n"], format="csv")`); out.Result != "\"Name,n\\nAnna,3\\n\\\"Carl, Jr.\\\",1\\n\"" {
		t.Fatalf("table csv: %q", out.Result)
	}
	if out := run(`result = math.floor(2.7) + time.parse_time("2026-05-03", format="2006-01-02").year`); out.Result != "2028" {
		t.Fatalf("math/time: %+v", out)
	}
	if out, _ := (&Server{}).calc(tc, CalcIn{Code: `result = time.now()`}); out.Error == "" {
		t.Errorf("time.now should not exist")
	}
	if out, _ := (&Server{}).calc(tc, CalcIn{Code: `table([["a"]])`}); !strings.Contains(out.Error, "columns") {
		t.Errorf("list rows without columns: %+v", out)
	}
}

// The tools a script can call are the ones its description names.
func TestScriptToolNames(t *testing.T) {
	s := &Server{}
	if got := strings.Join(s.scriptToolNames(), ","); got != "extract,list_collections,list_documents,read_document,search_documents" {
		t.Fatalf("names: %s", got)
	}
	if got := strings.Join(s.TextLocal().scriptToolNames(), ","); strings.Contains(got, "read_document") {
		t.Fatalf("text-local script tools include read_document: %s", got)
	}
}
