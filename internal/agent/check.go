package agent

// The check tool: a document against the rules of another document. The
// local model does what needs reading: it lists the checkable rules of the
// reference document, each with a variable and a condition over it, and
// looks the values of the variables up in the checked document. Whether a
// rule is met is decided by evaluating the condition (Starlark), not by the
// model, so a limit of 60 pages is never "met" with 85.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dbis-ilm/askyourdocs"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const (
	verdictMet        = "met"
	verdictViolated   = "violated"
	verdictCannotTell = "cannot tell"
)

type CheckIn struct {
	DocumentID          int64  `json:"document_id" jsonschema:"the document to check, id from list_documents or search_documents"`
	ReferenceDocumentID int64  `json:"reference_document_id" jsonschema:"the document with the rules (regulations, guide, policy); with rules in several documents, call check once per rule document"`
	Instruction         string `json:"instruction,omitempty" jsonschema:"which rules count, e.g. 'only the rules on length and sources'; default: all checkable rules"`
	Pages               string `json:"pages,omitempty" jsonschema:"only these pages of the checked document, if it has pages"`
	ReferencePages      string `json:"reference_pages,omitempty" jsonschema:"only these pages of the rule document, if it has pages"`
}

type RuleResult struct {
	Rule    string `json:"rule"`
	Where   string `json:"where,omitempty" jsonschema:"where the rule stands in the rule document"`
	Verdict string `json:"verdict" jsonschema:"met, violated, or cannot tell when the rule is no number or presence rule or its value was not found"`
	Reason  string `json:"reason"`
	Page    string `json:"page,omitempty" jsonschema:"page of the checked document where the value was found"`
}

type CheckOut struct {
	Document   DocumentOut  `json:"document"`
	Reference  DocumentOut  `json:"reference"`
	Results    []RuleResult `json:"results"`
	Met        int          `json:"met"`
	Violated   int          `json:"violated"`
	CannotTell int          `json:"cannot_tell"`
	Note       string       `json:"note,omitempty"`
}

// checkRule is one rule as the model lists it.
type checkRule struct {
	Rule      string `json:"rule"`
	Where     string `json:"where"`
	Kind      string `json:"kind"` // number | presence | other
	Variable  string `json:"variable"`
	Question  string `json:"question"`
	Condition string `json:"condition"`
}

var variableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// checkable reports whether the rule has what the evaluation needs: a kind
// with a value, a variable, a question and a condition that uses it.
func (r checkRule) checkable() bool {
	return (r.Kind == "number" || r.Kind == "presence") && variableName.MatchString(r.Variable) &&
		strings.TrimSpace(r.Question) != "" && len(r.Condition) <= 200 && strings.Contains(r.Condition, r.Variable)
}

// fact is a value found for a variable in one part of the document.
type fact struct {
	value any
	page  string
}

func (s *Server) check(ctx context.Context, in CheckIn) (CheckOut, outcome, error) {
	if !s.eng.HasModel() {
		return CheckOut{}, outcome{}, errors.New("no answer model is set up in PersonalRAG; compare the documents with read_document")
	}
	if in.DocumentID == in.ReferenceDocumentID {
		return CheckOut{}, outcome{}, errors.New("document_id and reference_document_id are the same document")
	}
	doc, err := s.sharedDocument(ctx, in.DocumentID)
	if err != nil {
		return CheckOut{}, outcome{}, err
	}
	ref, err := s.sharedDocument(ctx, in.ReferenceDocumentID)
	if err != nil {
		return CheckOut{}, outcome{}, err
	}
	docText, docNote, err := documentText(ctx, doc, in.Pages)
	if err != nil {
		return CheckOut{}, outcome{}, err
	}
	refText, refNote, err := documentText(ctx, ref, in.ReferencePages)
	if err != nil {
		return CheckOut{}, outcome{}, fmt.Errorf("rule document: %w", err)
	}
	colls := s.collections(ctx)
	out := CheckOut{Document: toDocumentOut(doc, colls), Reference: toDocumentOut(ref, colls), Results: []RuleResult{}, Note: strings.TrimSpace(docNote + " " + refNote)}

	rules, err := s.collectRules(ctx, ref.ID, filepath.Base(ref.Path), in.Instruction, askyourdocs.SplitText(refText, extractPartChars))
	if err != nil {
		return CheckOut{}, outcome{}, err
	}
	var asked []checkRule
	seen := map[string]bool{}
	for _, r := range rules {
		if r.checkable() && !seen[r.Variable] {
			seen[r.Variable] = true
			asked = append(asked, r)
		}
	}
	facts, err := s.collectFacts(ctx, doc.ID, filepath.Base(doc.Path), asked, askyourdocs.SplitText(docText, extractPartChars))
	if err != nil {
		return CheckOut{}, outcome{}, err
	}

	for _, r := range rules {
		res := RuleResult{Rule: r.Rule, Where: r.Where}
		if r.checkable() {
			res.Verdict, res.Reason, res.Page = evaluate(r, facts[r.Variable])
		} else {
			res.Verdict, res.Reason = verdictCannotTell, "not a rule with a number or something that must be present; check it by reading the document"
		}
		switch res.Verdict {
		case verdictMet:
			out.Met++
		case verdictViolated:
			out.Violated++
		default:
			out.CannotTell++
		}
		out.Results = append(out.Results, res)
	}
	if len(rules) == 0 {
		out.Note = strings.TrimSpace(out.Note + " No checkable rules found in the rule document.")
	}
	sum := fmt.Sprintf("%s against %s: %d met, %d violated, %d cannot tell", filepath.Base(doc.Path), filepath.Base(ref.Path), out.Met, out.Violated, out.CannotTell)
	return out, outcome{summary: sum}, nil
}

// collectRules lists the checkable rules of the rule document, part by
// part, without duplicates.
func (s *Server) collectRules(ctx context.Context, refID int64, name, scope string, parts []string) ([]checkRule, error) {
	var rules []checkRule
	seen := map[string]bool{}
	for i, p := range parts {
		label := fmt.Sprintf("Teil %d von %d", i+1, len(parts))
		raw, _, err := s.modelPart(ctx, extractKey(s.eng.ModelName(), "check_rules\n"+scope, p), refID, func(ctx context.Context) (string, error) {
			return s.eng.CheckRulesPart(ctx, scope, name, label, p)
		})
		if err != nil {
			return nil, fmt.Errorf("rule document, part %d: %w", i+1, err)
		}
		var got []checkRule
		if err := parseJSON(raw, '[', ']', &got); err != nil {
			slog.Warn("agent: check: rules not readable", "part", i+1, "err", err)
			continue
		}
		for _, r := range got {
			r.Rule, r.Kind = strings.TrimSpace(r.Rule), strings.ToLower(strings.TrimSpace(r.Kind))
			key := strings.ToLower(strings.Join(strings.Fields(r.Rule), " "))
			if key != "" && !seen[key] {
				seen[key] = true
				rules = append(rules, r)
			}
		}
	}
	return rules, nil
}

// collectFacts asks, part by part, for the values of the rules' variables.
func (s *Server) collectFacts(ctx context.Context, docID int64, name string, rules []checkRule, parts []string) (map[string][]fact, error) {
	facts := map[string][]fact{}
	if len(rules) == 0 {
		return facts, nil
	}
	var qs strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&qs, "%s: %s\n", r.Variable, strings.Join(strings.Fields(r.Question), " "))
	}
	questions := qs.String()
	for i, p := range parts {
		label := fmt.Sprintf("Teil %d von %d", i+1, len(parts))
		raw, _, err := s.modelPart(ctx, extractKey(s.eng.ModelName(), "check_facts\n"+questions, p), docID, func(ctx context.Context) (string, error) {
			return s.eng.CheckFactsPart(ctx, questions, name, label, p)
		})
		if err != nil {
			return nil, fmt.Errorf("document, part %d: %w", i+1, err)
		}
		var got map[string]struct {
			Value any `json:"value"`
			Page  any `json:"page"`
		}
		if err := parseJSON(raw, '{', '}', &got); err != nil {
			slog.Warn("agent: check: facts not readable", "part", i+1, "err", err)
			continue
		}
		for _, r := range rules {
			if g, ok := got[r.Variable]; ok && g.Value != nil {
				page := ""
				if g.Page != nil {
					page = strings.Trim(fmt.Sprint(g.Page), " ")
				}
				facts[r.Variable] = append(facts[r.Variable], fact{value: g.Value, page: page})
			}
		}
	}
	return facts, nil
}

// evaluate decides a rule from the values found for its variable. A number
// rule is met or violated if the condition says so for every value found;
// different verdicts for different values, or no number, are "cannot tell".
// A presence rule with nothing found is violated: the thing is not there.
func evaluate(r checkRule, found []fact) (verdict, reason, page string) {
	var values []starlark.Value
	var shown, pages []string
	for _, f := range found {
		v, text := starlarkFact(r.Kind, f.value)
		if v == nil {
			continue
		}
		values = append(values, v)
		shown = append(shown, text)
		pages = append(pages, f.page)
	}
	if r.Kind == "presence" {
		if len(values) == 0 {
			values, shown, pages = []starlark.Value{starlark.False}, []string{"not found in any part of the document"}, []string{""}
		} else {
			values, shown, pages = values[:1], []string{"found"}, pages[:1]
		}
	}
	if len(values) == 0 {
		return verdictCannotTell, "no value found in the document for: " + r.Question, ""
	}
	var met, violated []string
	for i, v := range values {
		ok, err := evalCondition(r.Condition, r.Variable, v)
		if err != nil {
			return verdictCannotTell, fmt.Sprintf("condition %q cannot be evaluated: %v", r.Condition, err), ""
		}
		desc := shown[i]
		if pages[i] != "" {
			desc += " (page " + pages[i] + ")"
		}
		if ok {
			met = append(met, desc)
		} else {
			violated = append(violated, desc)
		}
	}
	switch {
	case len(violated) == 0:
		return verdictMet, fmt.Sprintf("%s: %s; condition: %s", r.Variable, strings.Join(met, ", "), r.Condition), pages[0]
	case len(met) == 0:
		return verdictViolated, fmt.Sprintf("%s: %s; condition: %s", r.Variable, strings.Join(violated, ", "), r.Condition), firstNonEmpty(pages...)
	}
	return verdictCannotTell, fmt.Sprintf("values found differ: %s meet the condition %s, %s do not", strings.Join(met, ", "), r.Condition, strings.Join(violated, ", ")), ""
}

// starlarkFact converts a value the model found to Starlark, or nil when it
// does not fit the rule's kind.
func starlarkFact(kind string, v any) (starlark.Value, string) {
	switch kind {
	case "number":
		var f float64
		switch x := v.(type) {
		case float64:
			f = x
		case string:
			var err error
			if f, err = strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", "."), 64); err != nil {
				return nil, ""
			}
		default:
			return nil, ""
		}
		text := strconv.FormatFloat(f, 'f', -1, 64)
		if f == float64(int64(f)) {
			return starlark.MakeInt64(int64(f)), text
		}
		return starlark.Float(f), text
	case "presence":
		if b, ok := v.(bool); ok && b {
			return starlark.True, "found"
		}
		if s, ok := v.(string); ok && strings.EqualFold(strings.TrimSpace(s), "true") {
			return starlark.True, "found"
		}
	}
	return nil, ""
}

// evalCondition evaluates a rule's condition with the variable set. The
// expression has the Starlark builtins and a step limit, nothing else.
func evalCondition(condition, variable string, value starlark.Value) (bool, error) {
	thread := &starlark.Thread{Name: "check"}
	thread.SetMaxExecutionSteps(100_000)
	v, err := starlark.EvalOptions(&syntax.FileOptions{}, thread, "condition", condition, starlark.StringDict{variable: value})
	if err != nil {
		return false, err
	}
	b, ok := v.(starlark.Bool)
	if !ok {
		return false, fmt.Errorf("gives %s, not true or false", v.Type())
	}
	return bool(b), nil
}

// parseJSON reads the JSON value between the first open and the last close
// byte of s, so that text or code fences around it do not matter.
func parseJSON(s string, open, close byte, v any) error {
	i, j := strings.IndexByte(s, open), strings.LastIndexByte(s, close)
	if i < 0 || j < i {
		return errors.New("no JSON in the answer")
	}
	return json.Unmarshal([]byte(s[i:j+1]), v)
}

// modelPart returns the cached model answer for key, or runs the model and
// caches the answer. The call finishes even if ctx is cancelled, so a retry
// finds the result; no new call starts after a cancel.
func (s *Server) modelPart(ctx context.Context, key string, documentID int64, run func(context.Context) (string, error)) (answer string, cached bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	r, hit, err := s.st.CachedExtract(ctx, key)
	if err != nil || hit {
		return r, hit, err
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), partTimeout)
	defer cancel()
	if r, err = run(pctx); err != nil {
		return "", false, err
	}
	if err := s.st.PutExtract(context.WithoutCancel(ctx), key, documentID, r); err != nil {
		slog.Warn("agent: caching model answer failed", "err", err)
	}
	return r, false, nil
}
