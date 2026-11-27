package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firebase/genkit/go/ai"

	"github.com/dbis-ilm/personalrag/internal/testutil"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := Open(path, testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func addDoc(t *testing.T, s *Store, coll, path string, texts ...string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.UpsertDocument(ctx, Document{Path: path, CollectionID: coll, Status: StatusRunning, Format: "Text"})
	if err != nil {
		t.Fatal(err)
	}
	var docs []*ai.Document
	for _, txt := range texts {
		docs = append(docs, ai.DocumentFromText(txt, map[string]any{
			MetaDocumentID: id, MetaCollectionID: coll, MetaParentText: "Kontext: " + txt, MetaPages: "1", MetaPath: path,
		}))
	}
	if err := s.Index(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDocumentState(ctx, id, StatusOK, "", "h"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIndexRetrieveDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	for _, c := range []Collection{{ID: "a", Name: "A", Color: "#000", Folders: []string{"/docs/a"}}, {ID: "b", Name: "B", Color: "#111"}} {
		if err := s.CreateCollection(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	miet := addDoc(t, s, "a", "/docs/a/miet.txt", "Die Kündigungsfrist beträgt drei Monate zum Monatsende", "Die Miete ist monatlich im Voraus zu zahlen")
	addDoc(t, s, "b", "/docs/b/zahn.txt", "Der Selbstbehalt der Zahnzusatzversicherung beträgt zwanzig Prozent")

	hits, err := s.Retrieve(ctx, "Kündigungsfrist drei Monate", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Text, "Kündigungsfrist") {
		t.Fatalf("vector search: got %+v", hits)
	}
	if hits[0].ParentText == "" || hits[0].Path != "/docs/a/miet.txt" || hits[0].Pages != "1" {
		t.Fatalf("hit fields not populated: %+v", hits[0])
	}

	hits, err = s.Retrieve(ctx, "Selbstbehalt", 5, "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.CollectionID != "a" {
			t.Fatalf("collection filter leaked %+v", h)
		}
	}

	// Prefix match: "kündigung" finds "Kündigungsfrist"; diacritics folded.
	kw, err := s.KeywordSearch(ctx, "Wann ist die Kundigung möglich?", 5, "", GermanStopWords)
	if err != nil {
		t.Fatal(err)
	}
	if len(kw) != 1 || !strings.Contains(kw[0].Text, "Kündigungsfrist") {
		t.Fatalf("keyword search: got %+v", kw)
	}

	if coll, ok, _ := s.CollectionForPath(ctx, "/docs/a/sub/x.pdf"); !ok || coll != "a" {
		t.Fatalf("CollectionForPath = %q %v", coll, ok)
	}

	n, err := s.DeleteByURL(ctx, "/docs/a/miet.txt")
	if err != nil || n != 2 {
		t.Fatalf("DeleteByURL = %d, %v", n, err)
	}
	if kw, _ := s.KeywordSearch(ctx, "Kündigungsfrist", 5, "", nil); len(kw) != 0 {
		t.Fatalf("fts rows left after delete: %+v", kw)
	}
	if hits, _ := s.Retrieve(ctx, "Kündigungsfrist", 5, "a"); len(hits) != 0 {
		t.Fatalf("vectors left after delete: %+v", hits)
	}
	if d, _ := s.DocumentByID(ctx, miet); d.Chunks != 0 {
		t.Fatalf("chunk count not reset: %d", d.Chunks)
	}

	colls, err := s.Collections(ctx)
	if err != nil || len(colls) != 2 || colls[0].DocCount != 1 || len(colls[0].Folders) != 1 {
		t.Fatalf("Collections = %+v, %v", colls, err)
	}
	if err := s.DeleteCollection(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if docs, _ := s.Documents(ctx, "", ""); len(docs) != 1 {
		t.Fatalf("documents after DeleteCollection: %d", len(docs))
	}
}

func TestEmbeddingMismatch(t *testing.T) {
	s, path := openTest(t)
	s.Close()
	s2, err := Open(path, testutil.FakeEmbedder{}, "other", testutil.FakeDim)
	if !errors.Is(err, ErrEmbeddingMismatch) {
		t.Fatalf("want ErrEmbeddingMismatch, got %v", err)
	}
	defer s2.Close()
	if err := s2.ResetVectors(); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	s3, err := Open(path, testutil.FakeEmbedder{}, "other", testutil.FakeDim)
	if err != nil {
		t.Fatalf("after reset: %v", err)
	}
	s3.Close()
}

func TestFTSQuery(t *testing.T) {
	got := FTSQuery(`Wo steht "drei Monate" zur Kündigung?`, GermanStopWords)
	want := `"drei monate" OR "drei"* OR "monate"* OR "kündigung"*`
	if got != want {
		t.Fatalf("FTSQuery = %s, want %s", got, want)
	}
}
