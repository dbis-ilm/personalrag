package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/personalrag/internal/history"
	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/store"
	"github.com/dbis-ilm/personalrag/internal/testutil"
)

// TestSearchShowMore pages through 20 matches: 8 at first, then 8 more,
// then the last 4, without repeats, and the stored turn keeps them all.
func TestSearchShowMore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "Protokolle")
	os.MkdirAll(root, 0o755)
	for i := range 20 {
		text := fmt.Sprintf("# Sitzung %d\n\nTOP %d: Haushalt %d beraten, Posten %d.\n", i, i, 2000+i, i*7)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("sitzung-%02d.md", i)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "p", Name: "Protokolle", Color: "#000", Folders: []string{root}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	g := genkit.Init(ctx, genkit.WithPromptFS(rag.PromptFS), genkit.WithPromptDir("prompts"))
	ix := indexer.New(g, st, nil, nil)
	go ix.Run(ctx)
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); ix.Progress().Active && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	eng, err := rag.New(g, st, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.New(st.DB(), "")
	if err != nil {
		t.Fatal(err)
	}
	s := &AppService{appCtx: ctx, core: &core{st: st, eng: eng, hist: hist}, searches: map[int64]cachedSearch{}}

	res, err := s.Search("", "Haushalt", "p", "search")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sources) != 8 || !res.HasMore {
		t.Fatalf("first page: %d sources, hasMore %v", len(res.Sources), res.HasMore)
	}
	seen := map[int64]bool{}
	for _, src := range res.Sources {
		seen[src.ChunkID] = true
	}
	total := len(res.Sources)
	for page, want := range []struct {
		n    int
		more bool
	}{{8, true}, {4, false}} {
		more, err := s.MoreSources(res.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		if len(more.Sources) != want.n || more.HasMore != want.more {
			t.Fatalf("page %d: %d sources, hasMore %v; want %d, %v", page+2, len(more.Sources), more.HasMore, want.n, want.more)
		}
		for i, src := range more.Sources {
			if seen[src.ChunkID] {
				t.Errorf("page %d repeats chunk %d", page+2, src.ChunkID)
			}
			seen[src.ChunkID] = true
			if src.N != total+i+1 {
				t.Errorf("page %d: source numbered %d, want %d", page+2, src.N, total+i+1)
			}
		}
		total += len(more.Sources)
	}

	conv, err := s.Conversation(res.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if got := conv.Turns[0]; len(got.Sources) != 20 || got.HasMore {
		t.Fatalf("stored turn: %d sources, hasMore %v", len(got.Sources), got.HasMore)
	}
	if more, err := s.MoreSources(res.TurnID); err != nil || len(more.Sources) != 0 || more.HasMore {
		t.Fatalf("after the end: %+v, %v", more, err)
	}
}
