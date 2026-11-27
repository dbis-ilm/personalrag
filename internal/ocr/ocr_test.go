package ocr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbis-ilm/askyourdocs"
)

func TestGroup(t *testing.T) {
	lines := []Line{
		{Text: "Protokoll", Y: 0.10, H: 0.02},
		{Text: "Beschluss: Der Haushalt wird", Y: 0.16, H: 0.02},
		{Text: "angenommen. Die Kündi-", Y: 0.185, H: 0.02},
		{Text: "gungsfrist beträgt drei Monate.", Y: 0.21, H: 0.02},
		{Text: "  ", Y: 0.23, H: 0.02},
		{Text: "Zweite Spalte oben", Y: 0.12, H: 0.02}, // back up the page: new column
		{Text: "Bindestrich-", Y: 0.30, H: 0.02},
		{Text: "Wort bleibt", Y: 0.325, H: 0.02},
	}
	got := Group(lines)
	want := []string{
		"Protokoll",
		"Beschluss: Der Haushalt wird angenommen. Die Kündigungsfrist beträgt drei Monate.",
		"Zweite Spalte oben",
		"Bindestrich-Wort bleibt",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Group =\n%q\nwant\n%q", got, want)
	}
}

func TestScanFixtureHasNoTextLayer(t *testing.T) {
	paras, err := askyourdocs.ExtractParagraphs("testdata/scan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if askyourdocs.HasTextLayer(paras) {
		t.Fatalf("fixture has a text layer: %+v", paras)
	}
}

func TestPDF(t *testing.T) {
	if !Available() {
		if _, err := PDF(context.Background(), "testdata/scan.pdf"); err != ErrUnavailable {
			t.Fatalf("want ErrUnavailable, got %v", err)
		}
		t.Skip("no text recognition on this system")
	}
	dir := t.TempDir()
	SetCacheDir(dir)
	defer SetCacheDir("")

	start := time.Now()
	paras, err := PDF(context.Background(), "testdata/scan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("2 pages recognised in %s", time.Since(start).Round(time.Millisecond))
	var all []string
	for _, p := range paras {
		all = append(all, p.Text)
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"Protokoll der Sitzung", "Anna Berger", "Haushalt", "Kündigungsfrist", "drei Monate", "Senatssaal"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if paras[0].PageNum != 1 || paras[len(paras)-1].PageNum != 2 {
		t.Errorf("page numbers wrong: first %d, last %d", paras[0].PageNum, paras[len(paras)-1].PageNum)
	}
	if !askyourdocs.HasTextLayer(paras) {
		t.Error("recognised text too short")
	}

	// Second call comes from the cache.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("cache entries: %v", entries)
	}
	start = time.Now()
	again, err := PDF(context.Background(), "testdata/scan.pdf")
	if err != nil || len(again) != len(paras) || time.Since(start) > 200*time.Millisecond {
		t.Errorf("cache miss: %d paragraphs, %v, %s", len(again), err, time.Since(start))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	SetCacheDir("")
	if _, err := PDF(ctx, "testdata/scan.pdf"); err == nil {
		t.Error("cancelled context not honoured")
	}
}
