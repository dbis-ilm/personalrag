package ingest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dbis-ilm/askyourdocs"
)

func TestExtractMarkdown(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.md")
	src := "# Wohnung\n\nEinleitung.\n\n## Kündigung\n\nSchriftlich mit Unterschrift.\nNicht per E-Mail.\n\n```\n# kein Heading\n```\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	paras, err := Extract(p)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, para := range paras {
		texts = append(texts, para.Text)
	}
	if len(paras) != 5 {
		t.Fatalf("got %d paragraphs: %q", len(paras), texts)
	}
	if !paras[2].Heading || paras[3].Breadcrumb != "Wohnung › Kündigung" {
		t.Fatalf("heading/breadcrumb wrong: %+v", paras)
	}
	if paras[4].Text != "```\n# kein Heading\n```" {
		t.Fatalf("fenced block mangled: %q", paras[4].Text)
	}
	if chunks := askyourdocs.BuildChunks(paras, 1200, 150); len(chunks) == 0 {
		t.Fatal("no chunks")
	}
}

func TestSkip(t *testing.T) {
	for name, want := range map[string]bool{".DS_Store": true, "~$Vertrag.docx": true, "a.pdf": false, "Brief.icloud": false, ".Brief.pdf.icloud": true} {
		if SkipFile(name) != want {
			t.Errorf("SkipFile(%q) = %v", name, !want)
		}
	}
	if !Supported("x/Y.PDF") || Supported("a.xlsx") {
		t.Error("Supported wrong")
	}
}

func TestPatterns(t *testing.T) {
	pats := []string{"*.tmp", "Archiv", "Entwurf*"}
	for name, want := range map[string]bool{"x.TMP": true, "archiv": true, "Archiv2": false, "Entwurf_Vertrag.pdf": true, "Vertrag.pdf": false} {
		if MatchesAny(name, pats) != want {
			t.Errorf("MatchesAny(%q) = %v", name, !want)
		}
	}
	for _, bad := range []string{"Archiv/2019", `a\\b`, "[x"} {
		if ValidatePattern(bad) == nil {
			t.Errorf("ValidatePattern(%q) accepted", bad)
		}
	}
	if ValidatePattern("*.bak") != nil {
		t.Error("valid pattern refused")
	}
}
