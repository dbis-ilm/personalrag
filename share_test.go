package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestMailtoURL(t *testing.T) {
	u, cut := mailtoURL("Frage: Kündigung & Frist?", "Antwort mit Umlaut ä [1].\n\nQuellen\n[1] Vertrag.pdf", "gekürzt", 1900)
	if cut || !strings.HasPrefix(u, "mailto:?subject=") || strings.Contains(u, "+") || strings.Contains(u, " ") {
		t.Fatalf("short mail: %s (cut %v)", u, cut)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(u, "mailto:?"))
	if err != nil || q.Get("subject") != "Frage: Kündigung & Frist?" || !strings.Contains(q.Get("body"), "[1] Vertrag.pdf") {
		t.Fatalf("round trip: %v %v", q, err)
	}

	long := strings.Repeat("Ein langer Absatz mit vielen Worten. ", 40) + "\n" + strings.Repeat("Noch mehr Text ü. ", 200)
	u, cut = mailtoURL("Betreff", long, "gekürzt", 1900)
	if !cut || len(u) > 1900 {
		t.Fatalf("long mail: %d chars, cut %v", len(u), cut)
	}
	q, _ = url.ParseQuery(strings.TrimPrefix(u, "mailto:?"))
	if !strings.HasSuffix(q.Get("body"), "[…] gekürzt") {
		t.Errorf("no truncation note: %q", q.Get("body"))
	}
}

func TestSanitizeFileName(t *testing.T) {
	for in, want := range map[string]string{
		"Wie lange ist die Frist?": "Wie lange ist die Frist",
		"a/b\\c:d*e":               "a b c d e",
		"   ":                      "PersonalRAG",
		strings.Repeat("x", 80):    strings.Repeat("x", 60),
		"Zeile\nzwei\t<Ende>.":     "Zeile zwei Ende",
	} {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
