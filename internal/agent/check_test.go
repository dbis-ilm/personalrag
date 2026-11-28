package agent

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"go.starlark.net/starlark"

	"github.com/dbis-ilm/personalrag/internal/rag"
)

var checkCalls atomic.Int64

const checkRulesJSON = `[
 {"rule": "Der Textteil umfasst höchstens 60 Seiten.", "where": "§ 1", "kind": "number", "variable": "pages", "question": "Wie viele Seiten hat der Textteil?", "condition": "pages <= 60"},
 {"rule": "Der Arbeit ist eine Eigenständigkeitserklärung beizufügen.", "where": "§ 2", "kind": "presence", "variable": "declaration", "question": "Gibt es eine Eigenständigkeitserklärung?", "condition": "declaration"},
 {"rule": "Die Arbeit ist in klarer Sprache zu verfassen.", "where": "§ 3", "kind": "other", "variable": "", "question": "", "condition": ""},
 {"rule": "Es sind mindestens 20 Quellen anzugeben.", "where": "§ 4", "kind": "number", "variable": "sources", "question": "Wie viele Quellen?", "condition": "20 <= x"}
]`

// checkModel plays the answer model of the check prompts: it lists the
// fixed rules for a rule document, and finds "Seiten: N" and a mention of
// the declaration in the checked document.
func checkModel(g *genkit.Genkit) ai.Model {
	pages := regexp.MustCompile(`Seiten: (\d+)`)
	return genkit.DefineModel(g, "test/check", &ai.ModelOptions{Supports: &ai.ModelSupports{Multiturn: true, SystemRole: true}}, func(ctx context.Context, req *ai.ModelRequest, _ ai.ModelStreamCallback) (*ai.ModelResponse, error) {
		checkCalls.Add(1)
		var prompt strings.Builder
		for _, m := range req.Messages {
			prompt.WriteString(m.Text())
		}
		p := prompt.String()
		answer := "[]"
		switch {
		case strings.Contains(p, "Regelwerk: "):
			if strings.Contains(p, "§ 1") {
				answer = checkRulesJSON
			}
		case strings.Contains(p, "Fragen (Name: Frage):"):
			facts := []string{}
			if m := pages.FindStringSubmatch(p); m != nil {
				facts = append(facts, `"pages": {"value": `+m[1]+`, "page": "2"}`)
			}
			if strings.Contains(p, "Eigenständigkeitserklärung gez.") {
				facts = append(facts, `"declaration": {"value": true, "page": "9"}`)
			}
			answer = "```json\n{" + strings.Join(facts, ", ") + "}\n```"
		}
		return &ai.ModelResponse{Message: ai.NewModelTextMessage(answer)}, nil
	})
}

func TestCheck(t *testing.T) {
	f := setupWith(t, map[string][]byte{
		"regeln.md": []byte("# Prüfungsordnung\n\n§ 1 Der Textteil umfasst höchstens 60 Seiten.\n\n§ 2 Eigenständigkeitserklärung beifügen.\n"),
		"lang.md":   []byte("# Arbeit A\n\nSeiten: 85\n\nKeine Erklärung.\n"),
		"kurz.md":   []byte("# Arbeit B\n\nSeiten: 50\n\nEigenständigkeitserklärung gez. J. Meier\n"),
	})
	eng, err := rag.New(f.g, f.st, checkModel(f.g), nil)
	if err != nil {
		t.Fatal(err)
	}
	server := New(f.st, eng, "test")
	ctx := context.Background()
	verdicts := func(out CheckOut) map[string]string {
		m := map[string]string{}
		for _, r := range out.Results {
			m[r.Where] = r.Verdict
		}
		return m
	}

	out, _, err := server.check(ctx, CheckIn{DocumentID: f.ids["lang.md"], ReferenceDocumentID: f.ids["regeln.md"]})
	if err != nil {
		t.Fatal(err)
	}
	// § 1: 85 > 60; § 2: no declaration; § 3: not checkable; § 4: the
	// condition does not use its variable, so it is not evaluated.
	want := map[string]string{"§ 1": verdictViolated, "§ 2": verdictViolated, "§ 3": verdictCannotTell, "§ 4": verdictCannotTell}
	if got := verdicts(out); len(got) != 4 || got["§ 1"] != want["§ 1"] || got["§ 2"] != want["§ 2"] || got["§ 3"] != want["§ 3"] || got["§ 4"] != want["§ 4"] {
		t.Fatalf("verdicts %v, want %v\n%+v", got, want, out.Results)
	}
	if out.Violated != 2 || out.CannotTell != 2 || out.Met != 0 {
		t.Fatalf("counts: %+v", out)
	}
	if r := out.Results[0]; !strings.Contains(r.Reason, "85") || r.Page != "2" {
		t.Fatalf("reason and page of the violated limit: %+v", r)
	}

	out, _, err = server.check(ctx, CheckIn{DocumentID: f.ids["kurz.md"], ReferenceDocumentID: f.ids["regeln.md"]})
	if err != nil {
		t.Fatal(err)
	}
	if got := verdicts(out); got["§ 1"] != verdictMet || got["§ 2"] != verdictMet || out.Met != 2 {
		t.Fatalf("short thesis: %v %+v", got, out)
	}

	// The same check again comes from the cache.
	before := checkCalls.Load()
	if _, _, err := server.check(ctx, CheckIn{DocumentID: f.ids["kurz.md"], ReferenceDocumentID: f.ids["regeln.md"]}); err != nil {
		t.Fatal(err)
	}
	if n := checkCalls.Load() - before; n != 0 {
		t.Fatalf("repeated check called the model %d times", n)
	}

	// Sharing applies to both documents.
	if _, _, err := server.check(ctx, CheckIn{DocumentID: f.ids["kurz.md"], ReferenceDocumentID: f.ids["geheim.md"]}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("private rule document: %v", err)
	}
	if _, _, err := server.check(ctx, CheckIn{DocumentID: f.ids["kurz.md"], ReferenceDocumentID: f.ids["kurz.md"]}); err == nil {
		t.Fatal("same document twice should fail")
	}
}

func TestEvaluate(t *testing.T) {
	num := checkRule{Kind: "number", Variable: "pages", Question: "q", Condition: "pages <= 60"}
	for _, c := range []struct {
		name  string
		rule  checkRule
		found []fact
		want  string
	}{
		{"met", num, []fact{{value: 50.0}}, verdictMet},
		{"violated", num, []fact{{value: 85.0}}, verdictViolated},
		{"number as text with comma", num, []fact{{value: "59,5"}}, verdictMet},
		{"all values violate", num, []fact{{value: 85.0}, {value: 90.0}}, verdictViolated},
		{"values differ", num, []fact{{value: 85.0}, {value: 50.0}}, verdictCannotTell},
		{"nothing found", num, nil, verdictCannotTell},
		{"text is no number", num, []fact{{value: "viele"}}, verdictCannotTell},
		{"bad condition", checkRule{Kind: "number", Variable: "pages", Question: "q", Condition: "pages <="}, []fact{{value: 1.0}}, verdictCannotTell},
		{"condition is no yes/no", checkRule{Kind: "number", Variable: "pages", Question: "q", Condition: "pages + 1"}, []fact{{value: 1.0}}, verdictCannotTell},
		{"unknown name", checkRule{Kind: "number", Variable: "pages", Question: "q", Condition: "pages <= limit"}, []fact{{value: 1.0}}, verdictCannotTell},
		{"present", checkRule{Kind: "presence", Variable: "declaration", Question: "q", Condition: "declaration"}, []fact{{value: true, page: "9"}}, verdictMet},
		{"absent", checkRule{Kind: "presence", Variable: "declaration", Question: "q", Condition: "declaration"}, nil, verdictViolated},
		{"forbidden item present", checkRule{Kind: "presence", Variable: "watermark", Question: "q", Condition: "not watermark"}, []fact{{value: true}}, verdictViolated},
		{"forbidden item absent", checkRule{Kind: "presence", Variable: "watermark", Question: "q", Condition: "not watermark"}, nil, verdictMet},
	} {
		if got, reason, _ := evaluate(c.rule, c.found); got != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got, reason, c.want)
		}
	}
	// An endless condition is stopped.
	if _, err := evalCondition("[x for x in range(10000000)] and True", "n", starlark.MakeInt(1)); err == nil {
		t.Error("runaway condition not stopped")
	}
}

func TestParseJSON(t *testing.T) {
	var rules []checkRule
	if err := parseJSON("Hier die Regeln:\n```json\n[{\"rule\": \"a\"}]\n```\n", '[', ']', &rules); err != nil || len(rules) != 1 {
		t.Fatalf("fenced array: %v %v", rules, err)
	}
	if err := parseJSON("keine Regeln", '[', ']', &rules); err == nil {
		t.Error("text without JSON accepted")
	}
}
