package agent

// Benchmark: can a model run the in-app agent loop over typical tasks?
// Opt-in, it needs Ollama (bge-m3 for embeddings) and takes minutes:
//
//	PERSONALRAG_AGENT_BENCH=1 go test ./internal/agent -run TestAgentBench -v -timeout 2h
//
// PERSONALRAG_AGENT_MODELS lists model specs (default: the local gemma4
// and qwen3.6); gpustack/<name> uses GPUSTACK_URL and GPUSTACK_API_KEY.
// PERSONALRAG_AGENT_THINK=1 turns reasoning on. A Markdown report goes to
// PERSONALRAG_AGENT_REPORT if set.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbis-ilm/askyourdocs"
	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/store"
)

var people = []string{"Anna Berger", "Carl Dietrich", "Eva Fischer", "Jonas Hahn", "Lena Koch", "Max Neumann", "Paul Richter", "Sophie Wagner"}

// protocols writes 12 minutes with known attendance; excused members and
// names in the running text must not count.
func protocols(t *testing.T, dir string, n int) map[string]int {
	counts := map[string]int{}
	for i := 1; i <= n; i++ {
		// Attendance rates differ per person (20–90 %), so the counts
		// differ and cannot be guessed from a pattern.
		rates := []int{90, 75, 60, 50, 40, 30, 85, 20}
		var present, excused []string
		for j, p := range people {
			if (i*2654435761+j*40503+i*j*97)%100 < rates[j] {
				present = append(present, p)
			} else {
				excused = append(excused, p)
			}
		}
		if len(excused) == 0 { // the text below names one excused member
			excused, present = present[len(present)-1:], present[:len(present)-1]
		}
		for _, p := range present {
			counts[p]++
		}
		body := fmt.Sprintf(`# Protokoll der %d. Sitzung des Fakultätsrats

Datum: %02d.%02d.2026

Anwesend: %s

Entschuldigt: %s

## TOP 1: Genehmigung der Tagesordnung

Die Tagesordnung wird ohne Änderungen genehmigt. %s weist auf die Frist für Haushaltsanträge hin.

## TOP 2: Berichte

Der Dekan berichtet über die Berufungsverfahren. Nach Rücksprache mit %s wird das Thema vertagt.
`, i, (i%27)+1, (i%12)+1, strings.Join(present, ", "), strings.Join(excused, ", "), excused[0], people[(i+2)%len(people)])
		write(t, filepath.Join(dir, fmt.Sprintf("protokoll-%02d.md", i)), body)
	}
	return counts
}

const script = `# Datenbanksysteme – Vorlesungsskript

## Kapitel 1: Einführung

Ein Datenbanksystem besteht aus der Datenbank und dem Datenbankmanagementsystem (DBMS). Es trennt die logische Sicht auf die Daten von ihrer physischen Speicherung (Datenunabhängigkeit). Die Drei-Ebenen-Architektur nach ANSI/SPARC unterscheidet externe, konzeptuelle und interne Ebene.

## Kapitel 2: Das relationale Modell

Eine Relation ist eine Menge von Tupeln über einem Relationenschema. Schlüssel identifizieren Tupel eindeutig; Fremdschlüssel sichern die referentielle Integrität. Die relationale Algebra kennt Selektion, Projektion, Verbund, Vereinigung und Differenz.

## Kapitel 3: Transaktionen

Eine Transaktion ist eine Folge von Operationen, die als logische Einheit ausgeführt wird. Transaktionen erfüllen die ACID-Eigenschaften: Atomarität (alles oder nichts), Konsistenz (von einem konsistenten Zustand in einen konsistenten Zustand), Isolation (nebenläufige Transaktionen beeinflussen sich nicht) und Dauerhaftigkeit (bestätigte Änderungen überleben Ausfälle).

Ein Schedule ist serialisierbar, wenn er äquivalent zu einer seriellen Ausführung derselben Transaktionen ist. Konfliktserialisierbarkeit prüft man mit dem Serialisierbarkeitsgraphen: Der Schedule ist genau dann konfliktserialisierbar, wenn der Graph azyklisch ist.

Das Zwei-Phasen-Sperrprotokoll (2PL) verlangt, dass eine Transaktion nach der ersten Freigabe einer Sperre keine neue Sperre mehr anfordert (Wachstums- und Schrumpfungsphase). 2PL garantiert Konfliktserialisierbarkeit, kann aber zu Verklemmungen (Deadlocks) führen. Deadlocks erkennt man mit einem Wartegraphen und löst sie durch Abbruch einer Transaktion.

Für die Dauerhaftigkeit schreibt das DBMS Änderungen zuerst in ein Log (Write-Ahead-Logging). Nach einem Absturz werden bestätigte Transaktionen wiederholt (Redo) und unbestätigte zurückgesetzt (Undo).

## Kapitel 4: Indexstrukturen

B+-Bäume sind balancierte Suchbäume, deren Blattknoten verkettet sind; sie unterstützen Punkt- und Bereichsanfragen in logarithmischer Zeit. Hash-Indexe eignen sich nur für Gleichheitsanfragen.
`

// rules and the thesis to check: two violations (length, missing
// declaration), the other rules are met.
var rules = map[string]string{
	"pruefungsordnung.md": `# Prüfungsordnung Informatik (Auszug)

§ 21 Bachelorarbeit
(1) Die Bearbeitungszeit der Bachelorarbeit beträgt höchstens sechs Monate.
(2) Der Umfang der Arbeit soll 40 bis 60 Seiten (ohne Anhang) betragen.
(3) Die Arbeit ist in deutscher Sprache abzufassen; mit Zustimmung des Prüfungsausschusses ist Englisch zulässig.
(4) Der Arbeit ist eine eidesstattliche Erklärung beizufügen, dass sie selbstständig verfasst wurde.
`,
	"leitfaden.md": `# Leitfaden für Abschlussarbeiten

Abgabe: Die Arbeit wird als PDF hochgeladen und zusätzlich in zwei gebundenen Exemplaren im Prüfungsamt abgegeben.
Gutachten: Die Arbeit wird von zwei Prüfenden begutachtet.
`,
}

const thesis = `# Bachelorarbeit Jana Meier: Effiziente Anfrageverarbeitung in Graphdatenbanken

Angaben zur Abgabe

- Bearbeitungszeit: 15.03.2026 bis 10.08.2026 (fünf Monate)
- Umfang: 85 Seiten ohne Anhang
- Sprache: Englisch; Zustimmung des Prüfungsausschusses vom 02.03.2026 liegt vor
- Abgabe: PDF hochgeladen am 10.08.2026, zwei gebundene Exemplare im Prüfungsamt abgegeben
- Gutachter: Prof. Dr. K. Sattler, Dr. M. Becker

Inhalt: Kapitel 1 Einleitung, Kapitel 2 Grundlagen, Kapitel 3 Verfahren, Kapitel 4 Evaluation, Kapitel 5 Zusammenfassung, Literaturverzeichnis.
`

func write(t *testing.T, path, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

type spikeCase struct {
	name  string
	task  string
	check func(answer string) (ok bool, note string)
}

func TestAgentBench(t *testing.T) {
	if os.Getenv("PERSONALRAG_AGENT_BENCH") == "" {
		t.Skip("set PERSONALRAG_AGENT_BENCH=1 (needs Ollama, takes minutes)")
	}
	models := strings.Split(envOr("PERSONALRAG_AGENT_MODELS", "ollama/gemma4:12b,ollama/qwen3.6:27b"), ",")
	ctx := context.Background()
	dir := t.TempDir()

	counts := protocols(t, filepath.Join(dir, "Protokolle"), 12)
	counts40 := protocols(t, filepath.Join(dir, "Senat"), 40)
	t.Logf("expected counts: 12 protocols %v; 40 protocols %v", counts, counts40)
	write(t, filepath.Join(dir, "Skript", "datenbanksysteme.md"), script)
	for name, body := range rules {
		write(t, filepath.Join(dir, "Regeln", name), body)
	}
	write(t, filepath.Join(dir, "Arbeiten", "bachelorarbeit-meier.md"), thesis)

	// Plugins can be initialised once, so every Genkit gets its own providers.
	newGenkit := func() (*genkit.Genkit, *askyourdocs.Providers) {
		p, err := askyourdocs.NewProviders(askyourdocs.ProviderConfig{
			OllamaHost: envOr("OLLAMA_HOST", "http://localhost:11434"), GPUStackURL: os.Getenv("GPUSTACK_URL"), GPUStackAPIKey: os.Getenv("GPUSTACK_API_KEY"), Timeout: 10 * time.Minute,
		}, nil, append([]string{"ollama/bge-m3"}, models...)...)
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
	ix := indexer.New(g, st, nil, nil)
	go ix.Run(ctx)
	for _, c := range []store.Collection{
		{ID: "protokolle", Name: "Protokolle", Folders: []string{filepath.Join(dir, "Protokolle")}},
		{ID: "senat", Name: "Senat", Folders: []string{filepath.Join(dir, "Senat")}},
		{ID: "skript", Name: "Skript", Folders: []string{filepath.Join(dir, "Skript")}},
		{ID: "regeln", Name: "Regeln", Folders: []string{filepath.Join(dir, "Regeln")}},
		{ID: "arbeiten", Name: "Arbeiten", Folders: []string{filepath.Join(dir, "Arbeiten")}},
	} {
		c.Color = "#000"
		if err := st.CreateCollection(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := ix.ScanCollection(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(5 * time.Minute); ix.Progress().Active && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}

	checkCounts := func(counts map[string]int) func(string) (bool, string) {
		return func(a string) (bool, string) {
			var wrong []string
			for _, p := range people {
				m := regexp.MustCompile(regexp.QuoteMeta(p) + `\D{0,40}?(\d+)`).FindStringSubmatch(a)
				if m == nil || m[1] != fmt.Sprint(counts[p]) {
					got := "fehlt"
					if m != nil {
						got = m[1]
					}
					wrong = append(wrong, fmt.Sprintf("%s: %s statt %d", p, got, counts[p]))
				}
			}
			return len(wrong) == 0, strings.Join(wrong, "; ")
		}
	}
	cases := []spikeCase{
		{
			name:  "Teilnehmer (40)",
			task:  "Erstelle mir aus allen Protokollen der Sammlung „Senat“ eine Liste der Teilnehmer (nur die Anwesenden) und zähle, an wie vielen Sitzungen jede Person teilgenommen hat.",
			check: checkCounts(counts40),
		},
		{
			name: "Teilnehmerhäufigkeit",
			task: "Erstelle mir aus allen Protokollen der Sammlung „Protokolle“ eine Liste der Teilnehmer (nur die Anwesenden) und zähle, an wie vielen Sitzungen jede Person teilgenommen hat.",
			check: func(a string) (bool, string) {
				var wrong []string
				for _, p := range people {
					m := regexp.MustCompile(regexp.QuoteMeta(p) + `\D{0,40}?(\d+)`).FindStringSubmatch(a)
					if m == nil || m[1] != fmt.Sprint(counts[p]) {
						got := "fehlt"
						if m != nil {
							got = m[1]
						}
						wrong = append(wrong, fmt.Sprintf("%s: %s statt %d", p, got, counts[p]))
					}
				}
				return len(wrong) == 0, strings.Join(wrong, "; ")
			},
		},
		{
			name: "Prüfungsfragen",
			task: "Erstelle 5 Prüfungsfragen mit Musterlösungen zu Kapitel 3 des Vorlesungsskripts Datenbanksysteme.",
			check: func(a string) (bool, string) {
				hits := 0
				for _, k := range []string{"ACID", "Atomarität", "Isolation", "serialisierbar", "Zwei-Phasen", "Deadlock", "Verklemmung", "Log"} {
					if strings.Contains(strings.ToLower(a), strings.ToLower(k)) {
						hits++
					}
				}
				offTopic := strings.Contains(a, "B+-Baum") || strings.Contains(a, "ANSI/SPARC")
				return hits >= 4 && !offTopic, fmt.Sprintf("%d Fachbegriffe aus Kapitel 3, fremde Kapitel: %v", hits, offTopic)
			},
		},
		{
			name: "Regelprüfung",
			task: "Prüfe die Bachelorarbeit von Jana Meier (Sammlung „Arbeiten“) gegen die Regeln aus der Prüfungsordnung und dem Leitfaden (Sammlung „Regeln“). Nenne für jede Regel, ob sie eingehalten ist, mit Begründung.",
			check: func(a string) (bool, string) {
				low := strings.ToLower(a)
				pages := strings.Contains(low, "85") && (strings.Contains(low, "60") || strings.Contains(low, "umfang"))
				decl := strings.Contains(low, "eidesstattlich") || strings.Contains(low, "erklärung")
				return pages && decl, fmt.Sprintf("Umfang erkannt: %v, Erklärung erwähnt: %v", pages, decl)
			},
		},
	}

	var report strings.Builder
	report.WriteString("| Modell | Aufgabe | Ergebnis | Schritte | Dauer | Hinweis |\n|---|---|---|---|---|---|\n")
	server := New(st, nil, "spike").InApp()
	for _, spec := range models {
		cfg := askyourdocs.GenerateConfig(spec, askyourdocs.GenerateOptions{NumCtx: 32768, KeepAlive: "30m", Think: os.Getenv("PERSONALRAG_AGENT_THINK") != ""})
		// A fresh Genkit per model: tools are registered once per instance.
		g2, p2 := newGenkit()
		model2, err := p2.Model(g2, spec)
		if err != nil {
			t.Fatal(err)
		}
		eng2, err := rag.New(g2, st, model2, cfg)
		if err != nil {
			t.Fatal(err)
		}
		server.eng = eng2
		tools := server.Tools(g2)
		for _, c := range cases {
			if only := os.Getenv("PERSONALRAG_AGENT_CASES"); only != "" && !strings.Contains(only, c.name) {
				continue
			}
			var mu sync.Mutex
			var steps []Step
			runCtx := WithSteps(ctx, func(s Step) {
				mu.Lock()
				steps = append(steps, s)
				mu.Unlock()
				t.Logf("  [%s] %s %s → %s %s (%s)", spec, s.Tool, s.Input, s.Summary, s.Error, s.Took.Round(time.Millisecond))
			})
			runCtx, cancel := context.WithTimeout(runCtx, 20*time.Minute)
			started := time.Now()
			answer, err := Run(runCtx, g2, model2, cfg, tools, c.task, 60)
			cancel()
			took := time.Since(started).Round(time.Second)
			ok, note := false, ""
			if err != nil {
				note = "Fehler: " + err.Error()
			} else {
				ok, note = c.check(answer)
			}
			result := "✗"
			if ok {
				result = "✓"
			}
			calcs := 0
			for _, s := range steps {
				if s.Tool == "calc" {
					calcs++
				}
			}
			t.Logf("=== %s / %s: %s in %s, %d steps (%d calc); %s\n%s", spec, c.name, result, took, len(steps), calcs, note, answer)
			fmt.Fprintf(&report, "| %s | %s | %s | %d (%d calc) | %s | %s |\n", spec, c.name, result, len(steps), calcs, took, strings.ReplaceAll(note, "|", "/"))
		}
	}
	t.Log("\n" + report.String())
	if p := os.Getenv("PERSONALRAG_AGENT_REPORT"); p != "" {
		os.WriteFile(p, []byte(report.String()), 0o644)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
