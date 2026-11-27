package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLargeTree checks plan item 2.1's question whether the watcher carries
// large folders: 2,000 folders with 40,000 files. Opt-in, as it writes
// 40,000 files: PERSONALRAG_LARGE_TREE=1 go test -run LargeTree -v ./internal/watcher
func TestLargeTree(t *testing.T) {
	if os.Getenv("PERSONALRAG_LARGE_TREE") == "" {
		t.Skip("set PERSONALRAG_LARGE_TREE=1")
	}
	root := tempRoot(t)
	for d := range 2000 {
		dir := filepath.Join(root, fmt.Sprintf("jahr-%02d", d/100), fmt.Sprintf("ordner-%04d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 20 {
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("datei-%02d.md", f)), []byte("x"), 0o644)
		}
	}

	c := newCollector()
	w, err := New(fast, c.add)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	began := time.Now()
	if err := w.SetRoots([]string{root}); err != nil {
		t.Fatal(err)
	}
	t.Logf("watching 2,000 folders / 40,000 files took %s to set up", time.Since(began).Round(time.Millisecond))
	time.Sleep(2 * time.Second) // let events from creating the tree drain
	c.reset()

	deep := filepath.Join(root, "jahr-19", "ordner-1999", "datei-19.md")
	began = time.Now()
	os.WriteFile(deep, []byte("geändert"), 0o644)
	c.waitFor(t, deep, 10*time.Second)
	t.Logf("change deep in the tree reported after %s", time.Since(began).Round(time.Millisecond))
}
