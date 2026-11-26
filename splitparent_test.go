package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitParent(t *testing.T) {
	long := strings.Repeat("Ein vorheriger Satz mit etwas Inhalt. ", 20)
	chunk := "Die Kündigungsfrist beträgt drei Monate."
	parent := "[Vertrag › § 5]\n\n" + long + chunk + " " + long
	before, hl, after := splitParent(parent, chunk)
	if hl != chunk {
		t.Fatalf("highlight = %q", hl)
	}
	for name, s := range map[string]string{"before": before, "after": after} {
		if utf8.RuneCountInString(s) > 300 || strings.Contains(s, "[Vertrag") {
			t.Errorf("%s too long or has heading prefix: %q", name, s)
		}
	}
	if !strings.HasPrefix(before, "… Ein vorheriger Satz") {
		t.Errorf("before should start at a sentence: %q", before)
	}
	if !strings.HasSuffix(after, "Inhalt. …") {
		t.Errorf("after should end at a sentence: %q", after)
	}
	// Chunk not found in parent: whole chunk, no context.
	if b, h, a := splitParent("anderer Text", chunk); b != "" || h != chunk || a != "" {
		t.Errorf("fallback wrong: %q %q %q", b, h, a)
	}
}
