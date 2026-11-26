package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"personalrag/internal/store"
	"personalrag/internal/testutil"
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

// waitIdle runs until the queue is empty or the deadline passes.
func waitIdle(t *testing.T, ix *Indexer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
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
