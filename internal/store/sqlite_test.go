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

// Migration 4 queues indexed PDFs again (new column-aware text) and drops
// their cached extract results; other documents are left alone.
func TestReindexPDFsMigration(t *testing.T) {
	ctx := context.Background()
	st, path := openTest(t)
	if err := st.CreateCollection(ctx, Collection{ID: "c", Name: "C", Color: "#000", Folders: []string{t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	pdf, _ := st.UpsertDocument(ctx, Document{Path: "/x/a.pdf", CollectionID: "c", Format: "PDF", Status: StatusPending})
	md, _ := st.UpsertDocument(ctx, Document{Path: "/x/b.md", CollectionID: "c", Format: "Markdown", Status: StatusPending})
	scan, _ := st.UpsertDocument(ctx, Document{Path: "/x/s.pdf", CollectionID: "c", Format: "PDF", Status: StatusPending})
	st.SetDocumentState(ctx, pdf, StatusOK, "", "h1")
	st.SetDocumentState(ctx, md, StatusOK, "", "h2")
	st.SetDocumentState(ctx, scan, StatusOCR, "@ocr", "")
	st.PutExtract(ctx, "k-pdf", pdf, "Anna")
	st.PutExtract(ctx, "k-md", md, "Bert")
	// Pretend the database was written before migration 4.
	if _, err := st.DB().ExecContext(ctx, `UPDATE schema_versions SET version = 3 WHERE component = 'index'`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err := Open(path, testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	status := func(id int64) string {
		d, err := st.DocumentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d.Status
	}
	if status(pdf) != StatusPending || status(md) != StatusOK || status(scan) != StatusOCR {
		t.Fatalf("statuses after migration: pdf %s, md %s, scan %s", status(pdf), status(md), status(scan))
	}
	if _, ok, _ := st.CachedExtract(ctx, "k-pdf"); ok {
		t.Error("extract cache of the PDF kept")
	}
	if _, ok, _ := st.CachedExtract(ctx, "k-md"); !ok {
		t.Error("extract cache of other documents dropped")
	}
}

func TestCollectionInstructions(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t)
	if err := st.CreateCollection(ctx, Collection{ID: "p", Name: "Protokolle", Color: "#000", Folders: []string{t.TempDir()}, Instructions: "Kurz."}); err != nil {
		t.Fatal(err)
	}
	if got := st.CollectionInstructions(ctx, "p"); got != "Kurz." {
		t.Fatalf("created: %q", got)
	}
	if err := st.SetCollectionInstructions(ctx, "p", "„Senat“ meint den Akademischen Senat."); err != nil {
		t.Fatal(err)
	}
	colls, _ := st.Collections(ctx)
	if len(colls) != 1 || colls[0].Instructions != "„Senat“ meint den Akademischen Senat." {
		t.Fatalf("collections: %+v", colls)
	}
	if st.SetCollectionInstructions(ctx, "x", "y") == nil {
		t.Error("unknown collection accepted")
	}
	if st.CollectionInstructions(ctx, "x") != "" || st.CollectionInstructions(ctx, "") != "" {
		t.Error("instructions for an unknown collection")
	}
}
