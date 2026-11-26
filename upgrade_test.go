package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"personalrag/internal/history"
	"personalrag/internal/migrate"
	"personalrag/internal/store"
	"personalrag/internal/testutil"
)

// TestUpgradeFromPrototype opens a database written by the first commit
// (b0695f2, before versioned migrations) and checks that nothing is lost.
func TestUpgradeFromPrototype(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")
	copyFile(t, "testdata/prototype-b0695f2.db", path)

	st, err := store.Open(path, testutil.FakeEmbedder{}, "ollama/fake", testutil.FakeDim)
	if err != nil {
		t.Fatalf("open legacy index: %v", err)
	}
	defer st.Close()
	h, err := history.New(st.DB(), "")
	if err != nil {
		t.Fatalf("open legacy history: %v", err)
	}

	for comp, want := range map[string]int{"index": 3, "history": 1} {
		if v, _ := migrate.Version(ctx, st.DB(), comp); v != want {
			t.Errorf("%s version = %d, want %d", comp, v, want)
		}
	}
	colls, _ := st.Collections(ctx)
	if len(colls) != 1 || colls[0].Name != "Verträge" || colls[0].DocCount != 2 {
		t.Fatalf("collections: %+v", colls)
	}
	hits, err := st.Retrieve(ctx, "Kündigungsfrist drei Monate", 1, "vertraege")
	if err != nil || len(hits) != 1 || hits[0].Text != "Die Kündigungsfrist beträgt drei Monate." {
		t.Fatalf("vector search after upgrade: %+v, %v", hits, err)
	}
	if kw, _ := st.KeywordSearch(ctx, "Miete", 5, "", store.GermanStopWords); len(kw) != 1 {
		t.Fatalf("keyword search after upgrade: %+v", kw)
	}
	_, turns, err := h.Get(ctx, "c1")
	if err != nil || len(turns) != 1 || turns[0].Answer != "Drei Monate [1]." {
		t.Fatalf("history after upgrade: %+v, %v", turns, err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	dst.Close()
}
