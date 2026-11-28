package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dbis-ilm/personalrag/internal/ingest"
	"github.com/dbis-ilm/personalrag/internal/ocr"
)

const committed = "../../demo"

var scanFingerprint = regexp.MustCompile(`layout:[0-9a-f]{64}`)

// The committed corpus must be what the generator writes; otherwise a
// benchmark would check answers against stale expectations.
func TestCorpusUpToDate(t *testing.T) {
	dir := t.TempDir()
	if err := generate(dir); err != nil {
		t.Fatal(err)
	}
	var want []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			want = append(want, rel)
		}
		return nil
	})
	var have []string
	filepath.WalkDir(committed, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() != "README.md" {
			rel, _ := filepath.Rel(committed, p)
			have = append(have, rel)
		}
		return nil
	})
	if !slices.Equal(want, have) {
		t.Fatalf("demo/ has %v, the generator writes %v; run go run ./cmd/demogen", have, want)
	}
	for _, rel := range want {
		a, _ := os.ReadFile(filepath.Join(dir, rel))
		b, _ := os.ReadFile(filepath.Join(committed, rel))
		// Scans carry their layout's fingerprint; their pixels may differ
		// between platforms.
		if fa := scanFingerprint.Find(a); fa != nil {
			if !bytes.Equal(fa, scanFingerprint.Find(b)) {
				t.Errorf("demo/%s is out of date; run go run ./cmd/demogen", filepath.ToSlash(rel))
			}
			continue
		}
		if !bytes.Equal(a, b) {
			t.Errorf("demo/%s is out of date; run go run ./cmd/demogen", filepath.ToSlash(rel))
		}
	}
}

func extract(t *testing.T, path string) []string {
	t.Helper()
	paras, _, err := ingest.Extract(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(paras))
	for i, p := range paras {
		out[i] = p.Text
	}
	return out
}

// listAfter returns the names of the paragraph starting with heading.
func listAfter(paras []string, heading string) []string {
	for _, p := range paras {
		lines := strings.Split(p, "\n")
		if strings.TrimSpace(lines[0]) == heading {
			var names []string
			for _, l := range lines[1:] {
				if l = strings.TrimSpace(l); l != "" && !strings.HasSuffix(l, ":") {
					names = append(names, personName(l))
				}
			}
			return names
		}
	}
	return nil
}

// The protocols extract the way the expectations assume: participants in
// a list of their own, apart from guests and the excused.
func TestProtocolsExtract(t *testing.T) {
	for _, s := range buildSessions() {
		if s.number == scanSession {
			continue
		}
		paras := extract(t, filepath.Join(committed, "protokolle", s.file()))
		var want []string
		for _, p := range s.present {
			want = append(want, personName(p))
		}
		if got := listAfter(paras, "Teilnehmer/-innen:"); !slices.Equal(got, want) {
			t.Errorf("%s participants:\n got %v\nwant %v", s.file(), got, want)
		}
		var guests []string
		for _, g := range s.printedGuests() {
			guests = append(guests, personName(g))
		}
		if got := listAfter(paras, "Gäste:"); !slices.Equal(got, guests) {
			t.Errorf("%s guests:\n got %v\nwant %v", s.file(), got, guests)
		}
	}
}

var carRE = regexp.MustCompile(`Dienstwagen|Firmenwagen|Dienstfahrzeug`)

func TestOffersExtract(t *testing.T) {
	offers := buildOffers()
	hits, _ := carFiles(offers)
	if len(hits) != 9 {
		t.Fatalf("%d offers with a company car", len(hits))
	}
	for _, o := range offers {
		text := strings.Join(extract(t, filepath.Join(committed, "angebote", o.file())), "\n")
		if got, want := carRE.MatchString(text), slices.Contains(hits, o.file()); got != want {
			t.Errorf("%s: offers a company car %v, want %v", o.file(), got, want)
		}
	}
}

// The scanned protocol has no text layer; with OCR its participants and
// the clubhouse topic are readable.
func TestScanNeedsOCR(t *testing.T) {
	if !ocr.Available() {
		t.Skip("no OCR on this system")
	}
	if testing.Short() {
		t.Skip("OCR takes about 25 s on first use")
	}
	var s session
	for _, x := range buildSessions() {
		if x.number == scanSession {
			s = x
		}
	}
	text := strings.Join(extract(t, filepath.Join(committed, "protokolle", s.file())), "\n")
	if !strings.Contains(text, "Vereinsheim") {
		t.Error("clubhouse topic not recognised")
	}
	for _, p := range s.present {
		if n := personName(p); !strings.Contains(text, n) {
			t.Errorf("participant %s not recognised", n)
		}
	}
}

// The notes extract with their section headings on the pages the table of
// contents names, and the page question's expectation matches.
func TestScriptExtract(t *testing.T) {
	_, pages := scriptPages()
	paras, _, err := ingest.Extract(context.Background(), filepath.Join(committed, "skript", scriptFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, num := range []string{"3.1", "3.3", "3.6", "6.2"} {
		found := false
		for _, p := range paras {
			if strings.HasPrefix(p.Text, num+" ") && p.PageNum == pages[num] {
				found = true
			}
		}
		if !found {
			t.Errorf("section %s not on page %d", num, pages[num])
		}
	}
	text := strings.Join(strings.Fields(strings.Join(extract(t, filepath.Join(committed, "skript", scriptFile)), " ")), " ")
	for _, s := range []string{"two-phase locking protocol (2PL)", "two-phase commit protocol (2PC)", "wait-for graph"} {
		if !strings.Contains(text, s) {
			t.Errorf("notes lack %q", s)
		}
	}
}

// The thesis shows its length only through the table of contents and has
// thesisSources references and no declaration of authorship.
func TestThesisExtract(t *testing.T) {
	text := strings.Join(extract(t, filepath.Join(committed, "pruefung", "arbeit", thesisFile)), "\n")
	if !strings.Contains(text, fmt.Sprintf("Anhang A: Messergebnisse\t%d", thesisBodyPages+1)) {
		t.Error("table of contents without the appendix page")
	}
	refs := 0
	for _, l := range strings.Split(text[strings.LastIndex(text, "Literaturverzeichnis"):], "\n") {
		if regexp.MustCompile(`\(\d{4}\):`).MatchString(l) {
			refs++
		}
	}
	if refs != thesisSources {
		t.Errorf("%d references, want %d", refs, thesisSources)
	}
	if strings.Contains(text, "Erklärung") {
		t.Error("thesis contains a declaration")
	}
	rules := strings.Join(extract(t, filepath.Join(committed, "pruefung", "regeln", regulationsFile)), "\n")
	for _, s := range []string{"40 bis 60 Seiten", "Eigenständigkeitserklärung"} {
		if !strings.Contains(rules, s) {
			t.Errorf("regulations lack %q", s)
		}
	}
}

func TestPersonName(t *testing.T) {
	for in, want := range map[string]string{
		"Herr Martin Albers (Vorsitz)":          "Martin Albers",
		"Herr Dr. Thomas Engel":                 "Thomas Engel",
		"Frau Renate Kühn (Kassenprüferin)":     "Renate Kühn",
		"Herr Tim Köhler (i. V. Frau Schubert)": "Tim Köhler",
		"Frau Lea Schubert":                     "Lea Schubert",
		"Frau Prof. Dr. Eva Kirchner (TOP 3)":   "Eva Kirchner",
	} {
		if got := personName(in); got != want {
			t.Errorf("personName(%q) = %q, want %q", in, got, want)
		}
	}
}
