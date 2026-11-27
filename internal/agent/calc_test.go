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
		out, err := calcTool(tc, CalcIn{Code: code})
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
	out, err := calcTool(tc, CalcIn{Code: `
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
	if out, _ := calcTool(tc, CalcIn{Code: `outputs.append(1)`}); !strings.Contains(out.Error, "frozen") {
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
