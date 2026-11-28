package agent

// Demo benchmark: runs the questions of demo/cases.yaml against the demo
// collections the way the app does, and checks the results. Opt-in; it
// needs Ollama (bge-m3 for embeddings) and takes minutes:
//
//	PERSONALRAG_DEMO_BENCH=1 go test ./internal/agent -run TestDemoBench -v -timeout 3h
//
// PERSONALRAG_BENCH_ANSWER_MODEL is the answer model (default
// ollama/gemma4:12b); it answers, rates sources and runs extract for the
// agent, as in the app. PERSONALRAG_AGENT_MODELS lists agent models
// (default: the answer model); gpustack/<name> uses GPUSTACK_URL and
// GPUSTACK_API_KEY. PERSONALRAG_BENCH_CASES limits the run to case ids
// containing one of its comma-separated parts. A Markdown report goes to
// PERSONALRAG_BENCH_REPORT if set.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbis-ilm/askyourdocs"
	"github.com/firebase/genkit/go/genkit"
	"github.com/goccy/go-yaml"

	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/store"
)

const (
	demoDir   = "../../demo"
	morePage  = 8  // sources per "show more", as in the app
	maxShown  = 96 // sources a turn shows at most, as in the app
	agentTurn = 60
)

type demoCase struct {
	ID       string `yaml:"id"`
	Folder   string `yaml:"folder"`
	Mode     string `yaml:"mode"`
	Question string `yaml:"question"`
	Expect   struct {
		Documents   []string       `yaml:"documents"`
		Distractors []string       `yaml:"distractors"`
		Contains    []string       `yaml:"contains"`
		Counts      map[string]int `yaml:"counts"`
		NotCounted  []string       `yaml:"not_counted"`
	} `yaml:"expect"`
}

func loadDemoCases(t *testing.T) []demoCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(demoDir, "cases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version int        `yaml:"version"`
		Cases   []demoCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 {
		t.Fatalf("cases.yaml version %d", f.Version)
	}
	return f.Cases
}

// The cases file must parse and name folders that exist; this runs always.
func TestDemoCasesParse(t *testing.T) {
	cases := loadDemoCases(t)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		if _, err := os.Stat(filepath.Join(demoDir, c.Folder)); err != nil {
			t.Errorf("%s: folder %s: %v", c.ID, c.Folder, err)
		}
		for _, d := range append(slices.Clone(c.Expect.Documents), c.Expect.Distractors...) {
			if _, err := os.Stat(filepath.Join(demoDir, c.Folder, d)); err != nil {
				t.Errorf("%s: %v", c.ID, err)
			}
		}
		if c.Mode == "agent" && len(c.Expect.Counts) == 0 {
			t.Errorf("%s: agent case without counts", c.ID)
		}
	}
}

// benchResult is one checked result.
type benchResult struct {
	ok      bool
	details []string
}

func (o *benchResult) fail(format string, args ...any) {
	o.ok = false
	o.details = append(o.details, fmt.Sprintf(format, args...))
}

func (o *benchResult) note(format string, args ...any) {
	o.details = append(o.details, fmt.Sprintf(format, args...))
}

// checkDocuments compares found documents with the expectation.
func checkDocuments(c demoCase, found []string, o *benchResult, what string) {
	var missing, wrong []string
	for _, d := range c.Expect.Documents {
		if !slices.Contains(found, d) {
			missing = append(missing, d)
		}
	}
	for _, d := range c.Expect.Distractors {
		if slices.Contains(found, d) {
			wrong = append(wrong, d)
		}
	}
	n := len(c.Expect.Documents)
	if len(missing) > 0 {
		o.fail("%s: %d/%d, fehlt %s", what, n-len(missing), n, strings.Join(missing, ", "))
	} else if n > 0 {
		o.note("%s: %d/%d", what, n, n)
	}
	if len(wrong) > 0 {
		o.note("Ablenker: %s", strings.Join(wrong, ", "))
	}
}

func checkContains(c demoCase, answer string, o *benchResult) {
	low := strings.ToLower(answer)
	var missing []string
	for _, s := range c.Expect.Contains {
		if !strings.Contains(low, strings.ToLower(s)) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		o.fail("Antwort enthält nicht: %s", strings.Join(missing, ", "))
	}
}

var digitRE = regexp.MustCompile(`\d`)

// checkCounts finds each person's number in the answer (the first number
// within 40 non-digit characters after the name) and flags guests listed
// with a number.
func checkCounts(c demoCase, answer string, o *benchResult) {
	var wrong []string
	names := make([]string, 0, len(c.Expect.Counts))
	for n := range c.Expect.Counts {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		want := c.Expect.Counts[name]
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `\D{0,40}?(\d+)`).FindStringSubmatch(answer)
		switch {
		case m == nil:
			wrong = append(wrong, name+": fehlt")
		case m[1] != strconv.Itoa(want):
			wrong = append(wrong, fmt.Sprintf("%s: %s statt %d", name, m[1], want))
		}
	}
	if len(wrong) > 0 {
		o.fail("%d/%d richtig; %s", len(names)-len(wrong), len(names), strings.Join(wrong, "; "))
	} else {
		o.note("%d/%d richtig", len(names), len(names))
	}
	var guests []string
	for _, line := range strings.Split(answer, "\n") {
		low := strings.ToLower(line)
		if !digitRE.MatchString(line) || strings.Contains(low, "gast") || strings.Contains(low, "gäste") || strings.Contains(low, "guest") {
			continue
		}
		for _, g := range c.Expect.NotCounted {
			if strings.Contains(line, g) && !slices.Contains(guests, g) {
				guests = append(guests, g)
			}
		}
	}
	if len(guests) > 0 {
		o.fail("Gäste mitgezählt: %s", strings.Join(guests, ", "))
	}
}

var citeRE = regexp.MustCompile(`\[(\d+)\]`)

// cited maps the source numbers cited in text to document names.
func cited(text string, results []rag.Result) []string {
	byN := map[int]string{}
	for _, r := range results {
		byN[r.N] = filepath.Base(r.Hit.Path)
	}
	var out []string
	for _, m := range citeRE.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		if d, ok := byN[n]; ok && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

func docs(results []rag.Result) []string {
	var out []string
	for _, r := range results {
		if d := filepath.Base(r.Hit.Path); !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

func TestDemoBench(t *testing.T) {
	if os.Getenv("PERSONALRAG_DEMO_BENCH") == "" {
		t.Skip("set PERSONALRAG_DEMO_BENCH=1 (needs Ollama, takes minutes)")
	}
	answerSpec := envOr("PERSONALRAG_BENCH_ANSWER_MODEL", "ollama/gemma4:12b")
	agentSpecs := strings.Split(envOr("PERSONALRAG_AGENT_MODELS", answerSpec), ",")
	cases := loadDemoCases(t)
	if only := os.Getenv("PERSONALRAG_BENCH_CASES"); only != "" {
		cases = slices.DeleteFunc(cases, func(c demoCase) bool {
			for _, part := range strings.Split(only, ",") {
				if strings.Contains(c.ID, strings.TrimSpace(part)) {
					return false
				}
			}
			return true
		})
	}
	ctx := context.Background()
	dir := t.TempDir()

	newGenkit := func() (*genkit.Genkit, *askyourdocs.Providers) {
		p, err := askyourdocs.NewProviders(askyourdocs.ProviderConfig{
			OllamaHost: envOr("OLLAMA_HOST", "http://localhost:11434"), GPUStackURL: os.Getenv("GPUSTACK_URL"), GPUStackAPIKey: os.Getenv("GPUSTACK_API_KEY"), Timeout: 10 * time.Minute,
		}, nil, append([]string{"ollama/bge-m3", answerSpec}, agentSpecs...)...)
		if err != nil {
			t.Fatal(err)
		}
		return genkit.Init(ctx, genkit.WithPlugins(p.Plugins()...), genkit.WithPromptFS(rag.PromptFS), genkit.WithPromptDir("prompts")), p
	}
	g, providers := newGenkit()
	emb, err := providers.Embedder(g, "ollama/bge-m3", 1024)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "index.db"), emb, "bge-m3", 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Index each demo folder as a collection named after it.
	ix := indexer.New(g, st, nil, nil)
	go ix.Run(ctx)
	var folders []string
	for _, c := range cases {
		if !slices.Contains(folders, c.Folder) {
			folders = append(folders, c.Folder)
		}
	}
	started := time.Now()
	for _, f := range folders {
		abs, err := filepath.Abs(filepath.Join(demoDir, f))
		if err != nil {
			t.Fatal(err)
		}
		coll := store.Collection{ID: f, Name: f, Color: "#000", Folders: []string{abs}}
		if err := st.CreateCollection(ctx, coll); err != nil {
			t.Fatal(err)
		}
		if err := ix.ScanCollection(ctx, coll); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(15 * time.Minute); ix.Progress().Active && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("indexed %v in %s", folders, time.Since(started).Round(time.Second))

	answerCfg := askyourdocs.GenerateConfig(answerSpec, askyourdocs.GenerateOptions{NumCtx: 16384, KeepAlive: "30m"})
	answerModel, err := providers.Model(g, answerSpec)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rag.New(g, st, answerModel, answerCfg)
	if err != nil {
		t.Fatal(err)
	}

	var report strings.Builder
	report.WriteString("| Fall | Modus | Modell | Ergebnis | Details | Dauer |\n|---|---|---|---|---|---|\n")
	row := func(c demoCase, model string, o benchResult, took time.Duration) {
		result := "✗"
		if o.ok {
			result = "✓"
		}
		details := strings.ReplaceAll(strings.Join(o.details, "; "), "|", "/")
		t.Logf("=== %s [%s, %s]: %s in %s — %s", c.ID, c.Mode, model, result, took.Round(time.Second), details)
		fmt.Fprintf(&report, "| %s | %s | %s | %s | %s | %s |\n", c.ID, c.Mode, model, result, details, took.Round(time.Second))
	}

	for _, c := range cases {
		switch c.Mode {
		case "search":
			began := time.Now()
			o := benchResult{ok: true}
			res, err := eng.SearchN(ctx, c.Question, c.Folder, maxShown)
			if err != nil {
				o.fail("Fehler: %v", err)
			} else {
				first := res.Results[:min(rag.MaxSources, len(res.Results))]
				firstPage := benchResult{ok: true}
				checkDocuments(c, docs(first), &firstPage, "erste Seite")
				o.details = append(o.details, firstPage.details...)
				checkDocuments(c, docs(res.Results), &o, fmt.Sprintf("mit „Mehr anzeigen“ (%d Treffer)", len(res.Results)))
			}
			row(c, "–", o, time.Since(began))

		case "answer":
			began := time.Now()
			o := benchResult{ok: true}
			res, err := eng.SearchN(ctx, c.Question, c.Folder, maxShown)
			if err != nil {
				o.fail("Fehler: %v", err)
				row(c, answerSpec, o, time.Since(began))
				continue
			}
			first := res.Results[:min(rag.MaxSources, len(res.Results))]
			var text strings.Builder
			if _, err := eng.Answer(ctx, c.Question, "", first, func(s string) { text.WriteString(s) }); err != nil {
				o.fail("Fehler: %v", err)
				row(c, answerSpec, o, time.Since(began))
				continue
			}
			firstText := text.String()
			// "Show more" page by page over all matches, as the app's
			// "evaluate all further matches".
			pages := 1
			for i := len(first); i < len(res.Results); i += morePage {
				page := res.Results[i:min(i+morePage, len(res.Results))]
				more, err := eng.AnswerMore(ctx, c.Question, "", text.String(), page)
				if err != nil {
					o.fail("Fehler bei „Mehr anzeigen“: %v", err)
					break
				}
				pages++
				if more.Text != "" {
					text.WriteString("\n\n" + more.Text)
				}
			}
			firstPage := benchResult{ok: true}
			checkDocuments(c, cited(firstText, first), &firstPage, "zitiert in der ersten Antwort")
			o.details = append(o.details, firstPage.details...)
			checkDocuments(c, cited(text.String(), res.Results), &o, fmt.Sprintf("zitiert nach %d Seiten", pages))
			checkContains(c, text.String(), &o)
			row(c, answerSpec, o, time.Since(began))
			t.Logf("answer %s:\n%s", c.ID, text.String())

		case "agent":
			for _, spec := range agentSpecs {
				// A fresh Genkit per model: tools register once per instance;
				// extract runs with the answer model, as in the app.
				g2, p2 := newGenkit()
				agentModel, err := p2.Model(g2, spec)
				if err != nil {
					t.Fatal(err)
				}
				answer2, err := p2.Model(g2, answerSpec)
				if err != nil {
					t.Fatal(err)
				}
				eng2, err := rag.New(g2, st, answer2, answerCfg)
				if err != nil {
					t.Fatal(err)
				}
				server := New(st, eng2, "demo-bench").InApp()
				tools := server.Tools(g2)
				var mu sync.Mutex
				var steps []Step
				runCtx := WithSteps(ctx, func(s Step) {
					mu.Lock()
					steps = append(steps, s)
					mu.Unlock()
					t.Logf("  [%s] %s %s → %s %s (%s)", spec, s.Tool, s.Input, s.Summary, s.Error, s.Took.Round(time.Millisecond))
				})
				runCtx, cancel := context.WithTimeout(runCtx, 30*time.Minute)
				prompt := fmt.Sprintf("Work only with the collection %q (collection_id %q).\n\nTask: %s", c.Folder, c.Folder, c.Question)
				cfg := askyourdocs.GenerateConfig(spec, askyourdocs.GenerateOptions{NumCtx: 32768, KeepAlive: "30m"})
				began := time.Now()
				answer, err := Run(runCtx, g2, agentModel, cfg, tools, prompt, agentTurn)
				cancel()
				o := benchResult{ok: true}
				if err != nil {
					o.fail("Fehler: %v", err)
				} else {
					checkCounts(c, answer, &o)
					checkContains(c, answer, &o)
				}
				calcs := 0
				for _, s := range steps {
					if s.Tool == "calc" {
						calcs++
					}
				}
				o.note("%d Schritte, %d calc", len(steps), calcs)
				row(c, spec, o, time.Since(began))
				t.Logf("answer %s [%s]:\n%s", c.ID, spec, answer)
			}

		default:
			t.Errorf("%s: unknown mode %q", c.ID, c.Mode)
		}
	}
	t.Log("\n" + report.String())
	if p := os.Getenv("PERSONALRAG_BENCH_REPORT"); p != "" {
		if err := os.WriteFile(p, []byte(report.String()), 0o644); err != nil {
			t.Error(err)
		}
	}
}
