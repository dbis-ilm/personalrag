package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/personalrag/internal/ocr"
	"github.com/dbis-ilm/personalrag/internal/store"
	"github.com/dbis-ilm/personalrag/internal/testutil"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitIdle runs until the queue is empty or 10 s have passed.
func waitIdle(t *testing.T, ix *Indexer) {
	t.Helper()
	waitIdleFor(t, ix, 10*time.Second)
}

func waitIdleFor(t *testing.T, ix *Indexer, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if p := ix.Progress(); !p.Active {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("indexer did not finish")
}

func TestScanIndexUpdateDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "Verträge")
	write(t, filepath.Join(root, "miete.md"), "# Mietvertrag\n\nDie Kündigungsfrist beträgt drei Monate.\n")
	write(t, filepath.Join(root, "notiz.txt"), "Schlüssel liegen bei der Nachbarin.")
	write(t, filepath.Join(root, ".git", "config"), "ignored")
	write(t, filepath.Join(root, "tabelle.xlsx"), "unsupported")

	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "v", Name: "Verträge", Color: "#000", Folders: []string{root}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}

	ix := New(genkit.Init(ctx), st, nil, nil)
	go ix.Run(ctx)
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, ix)

	docs, _ := st.Documents(ctx, "v", "")
	if len(docs) != 2 {
		t.Fatalf("want 2 documents, got %+v", docs)
	}
	for _, d := range docs {
		if d.Status != store.StatusOK || d.Chunks == 0 || d.Hash == "" {
			t.Fatalf("document not indexed: %+v", d)
		}
	}
	hits, err := st.KeywordSearch(ctx, "Kündigungsfrist", 5, "v", store.GermanStopWords)
	if err != nil || len(hits) == 0 || hits[0].Breadcrumb != "Mietvertrag" {
		t.Fatalf("search after index: %+v, %v", hits, err)
	}

	// Change one file, delete the other, rescan.
	time.Sleep(1100 * time.Millisecond) // mtime has second resolution
	write(t, filepath.Join(root, "miete.md"), "# Mietvertrag\n\nDie Kündigungsfrist beträgt sechs Monate.\n")
	os.Remove(filepath.Join(root, "notiz.txt"))
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, ix)

	docs, _ = st.Documents(ctx, "v", "")
	if len(docs) != 1 {
		t.Fatalf("deleted file still indexed: %+v", docs)
	}
	hits, _ = st.KeywordSearch(ctx, "Kündigungsfrist", 5, "v", store.GermanStopWords)
	if len(hits) != 1 || !strings.Contains(hits[0].Text, "sechs") {
		t.Fatalf("changed file not re-indexed (or old chunks left): %+v", hits)
	}

	// Unchanged rescan queues nothing.
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	if p := ix.Progress(); p.Active {
		t.Fatalf("unchanged rescan queued work: %+v", p)
	}
}

func TestPause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "Text A")
	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "c", Name: "C", Color: "#000", Folders: []string{dir}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	ix := New(genkit.Init(ctx), st, nil, nil)
	ix.SetPaused(true)
	go ix.Run(ctx)
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if p := ix.Progress(); !p.Paused || p.Total != 1 || p.Done != 0 {
		t.Fatalf("paused indexer made progress: %+v", p)
	}
	ix.SetPaused(false)
	waitIdle(t, ix)
	if docs, _ := st.Documents(ctx, "c", store.StatusOK); len(docs) != 1 {
		t.Fatal("not indexed after resume")
	}
}

func TestExcludes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "Text A")
	write(t, filepath.Join(dir, "Archiv", "alt.txt"), "Alter Text")
	write(t, filepath.Join(dir, "notiz.tmp.txt"), "Temp")
	st, err := store.Open(filepath.Join(t.TempDir(), "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "c", Name: "C", Color: "#000", Folders: []string{dir}}
	st.CreateCollection(ctx, coll)
	ix := New(genkit.Init(ctx), st, nil, nil)
	go ix.Run(ctx)
	ix.ScanCollection(ctx, coll)
	waitIdle(t, ix)
	if docs, _ := st.Documents(ctx, "c", ""); len(docs) != 3 {
		t.Fatalf("before excludes: %d documents", len(docs))
	}
	ix.SetExcludes([]string{"archiv", "*.tmp.txt"})
	ix.ScanCollection(ctx, coll)
	waitIdle(t, ix)
	docs, _ := st.Documents(ctx, "c", "")
	if len(docs) != 1 || filepath.Base(docs[0].Path) != "a.txt" {
		t.Fatalf("after excludes: %+v", docs)
	}
}

// eventually polls cond until it holds or within has passed, and returns
// how long it took.
func eventually(t *testing.T, what string, within time.Duration, cond func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if cond() {
			return time.Since(start)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: not within %s", what, within)
	return 0
}

// TestWatchKeepsIndexCurrent is the acceptance test of plan item 2.1: a
// changed file is re-indexed within 10 s, without a periodic scan.
func TestWatchKeepsIndexCurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir, err := filepath.EvalSymlinks(t.TempDir()) // the app stores canonical folders
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "Protokolle")
	write(t, filepath.Join(root, "sitzung-1.md"), "# Sitzung 1\n\nBeschluss: Haushalt angenommen.\n")
	write(t, filepath.Join(root, "alt.md"), "# Alt\n\nVeraltete Notiz.\n")
	write(t, filepath.Join(root, "Archiv", "2019.md"), "# 2019\n\nAlter Beschluss.\n")

	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "p", Name: "Protokolle", Color: "#000", Folders: []string{root}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	ix := New(genkit.Init(ctx), st, nil, nil)
	ix.SetExcludes([]string{"Entwurf*"})
	go ix.Run(ctx)
	watching := make(chan error, 1)
	go func() { watching <- ix.Watch(ctx) }()
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, ix)
	time.Sleep(500 * time.Millisecond) // the event stream starts asynchronously

	found := func(word string) bool {
		hits, _ := st.KeywordSearch(ctx, word, 5, "p", store.GermanStopWords)
		return len(hits) > 0
	}
	docCount := func() int {
		docs, _ := st.Documents(ctx, "p", "")
		return len(docs)
	}

	// Edit in place.
	time.Sleep(1100 * time.Millisecond) // mtime has second resolution
	write(t, filepath.Join(root, "sitzung-1.md"), "# Sitzung 1\n\nBeschluss: Haushalt vertagt.\n")
	took := eventually(t, "edited file re-indexed", 10*time.Second, func() bool { return found("vertagt") && !found("angenommen") })
	t.Logf("edit re-indexed after %s", took.Round(10*time.Millisecond))

	// Atomic save, as many editors do: write a temp file, rename it over.
	tmp := filepath.Join(root, ".sitzung-1.md.swp")
	write(t, tmp, "# Sitzung 1\n\nBeschluss: Haushalt abgelehnt.\n")
	if err := os.Rename(tmp, filepath.Join(root, "sitzung-1.md")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "atomically saved file re-indexed", 10*time.Second, func() bool { return found("abgelehnt") })

	// New file, new folder with a file, excluded file.
	write(t, filepath.Join(root, "sitzung-2.md"), "# Sitzung 2\n\nBeschluss: Neubau geplant.\n")
	write(t, filepath.Join(root, "2026", "Mai", "sitzung-3.md"), "# Sitzung 3\n\nBeschluss: Sporthalle saniert.\n")
	write(t, filepath.Join(root, "Entwurf-4.md"), "# Entwurf\n\nGeheimplan.\n")
	eventually(t, "new file indexed", 10*time.Second, func() bool { return found("Neubau") })
	eventually(t, "file in new folder indexed", 10*time.Second, func() bool { return found("Sporthalle") })

	// Deleted file and deleted folder leave the index.
	os.Remove(filepath.Join(root, "alt.md"))
	os.RemoveAll(filepath.Join(root, "Archiv"))
	eventually(t, "deleted file and folder removed", 10*time.Second, func() bool { return !found("Veraltete") && !found("2019") })

	waitIdle(t, ix)
	if found("Geheimplan") {
		t.Error("excluded file was indexed")
	}
	if n := docCount(); n != 3 {
		docs, _ := st.Documents(ctx, "p", "")
		t.Errorf("want 3 documents (sitzung-1..3), got %d: %+v", n, docs)
	}

	cancel()
	select {
	case err := <-watching:
		if err != nil {
			t.Errorf("Watch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Watch did not return after cancel")
	}
}

// TestScannedPDF: with text recognition a scanned PDF becomes searchable
// (plan item 2.5); without it, it stays "needs OCR". A document marked
// "needs OCR" earlier is read on the next scan once recognition exists.
func TestScannedPDF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "Scans")
	scan, err := os.ReadFile("../ocr/testdata/scan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "protokoll.pdf"), string(scan))
	write(t, filepath.Join(root, "alt.pdf"), string(scan))

	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "s", Name: "Scans", Color: "#000", Folders: []string{root}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	// alt.pdf was marked "needs OCR" by an earlier version.
	info, _ := os.Stat(filepath.Join(root, "alt.pdf"))
	id, err := st.UpsertDocument(ctx, store.Document{Path: filepath.Join(root, "alt.pdf"), CollectionID: "s", Size: info.Size(), ModTime: info.ModTime().Unix(), Format: "PDF", Status: store.StatusOCR})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDocumentState(ctx, id, store.StatusOCR, NoteNeedsOCR, ""); err != nil {
		t.Fatal(err)
	}

	ix := New(genkit.Init(ctx), st, nil, nil)
	go ix.Run(ctx)
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	// Vision's first request in a new binary takes about 25 s (it prepares
	// its models once per executable); every page after that ~0.1 s.
	waitIdleFor(t, ix, 90*time.Second)

	docs, _ := st.Documents(ctx, "s", "")
	if len(docs) != 2 {
		t.Fatalf("want 2 documents, got %+v", docs)
	}
	if !ocr.Available() {
		for _, d := range docs {
			if d.Status != store.StatusOCR || d.Note != NoteNeedsOCR {
				t.Errorf("%s: want needs OCR, got %s %q", d.Path, d.Status, d.Note)
			}
		}
		return
	}
	for _, d := range docs {
		if d.Status != store.StatusOK || d.Note != NoteOCRRead || d.Chunks == 0 {
			t.Errorf("%s: not indexed from OCR: %+v", d.Path, d)
		}
	}
	hits, err := st.KeywordSearch(ctx, "Kündigungsfrist", 5, "s", store.GermanStopWords)
	if err != nil || len(hits) == 0 || !strings.HasPrefix(hits[0].Pages, "1") { // the small fixture's pages share a chunk: "1-2"
		t.Fatalf("recognised text not searchable with its page: %+v, %v", hits, err)
	}
}
