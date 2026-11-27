package rag

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/store"
)

// TestOllamaEndToEnd indexes two small documents with a real embedding model
// and answers a question with a real LLM. Opt in with PERSONALRAG_OLLAMA=1;
// models via PERSONALRAG_LLM / PERSONALRAG_EMBED (Ollama names).
func TestOllamaEndToEnd(t *testing.T) {
	if os.Getenv("PERSONALRAG_OLLAMA") == "" {
		t.Skip("set PERSONALRAG_OLLAMA=1 to run against a local Ollama")
	}
	host := "http://localhost:11434"
	llmName := envOr("PERSONALRAG_LLM", "gemma4:12b")
	embName := envOr("PERSONALRAG_EMBED", "bge-m3")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dim, err := EmbeddingDim(ctx, host, embName)
	if err != nil {
		t.Skipf("ollama not usable: %v", err)
	}
	llmSpec, embSpec := "ollama/"+llmName, "ollama/"+embName
	p, err := askyourdocs.NewProviders(askyourdocs.ProviderConfig{OllamaHost: host},
		[]string{askyourdocs.ProviderOllama}, llmSpec, embSpec)
	if err != nil {
		t.Fatal(err)
	}
	g := genkit.Init(ctx, genkit.WithPlugins(p.Plugins()...), genkit.WithPromptFS(PromptFS), genkit.WithPromptDir("prompts"))
	emb, err := p.Embedder(g, embSpec, dim)
	if err != nil {
		t.Fatal(err)
	}
	llm, err := p.Model(g, llmSpec)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	docs := filepath.Join(dir, "Wohnung")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "mietvertrag.md"), []byte(`# Mietvertrag Schillerstraße 12

## § 5 Kündigung

Die Kündigungsfrist für den Mieter beträgt drei Monate zum Ende eines Kalendermonats.
Die Kündigung bedarf der Schriftform und ist eigenhändig zu unterzeichnen.

## § 7 Haustiere

Kleintiere sind erlaubt. Hunde und Katzen nur nach schriftlicher Zustimmung.
`), 0o644)
	os.WriteFile(filepath.Join(docs, "hausordnung.txt"), []byte("Die Mülltonnen werden dienstags geleert. Ruhezeiten gelten von 22 bis 6 Uhr."), 0o644)

	st, err := store.Open(filepath.Join(dir, "index.db"), emb, embSpec, dim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "w", Name: "Wohnung", Color: "#000", Folders: []string{docs}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(g, st, nil, nil)
	go ix.Run(ctx)
	if err := ix.ScanCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	for ix.Progress().Active {
		time.Sleep(100 * time.Millisecond)
	}
	if ok, _ := st.Documents(ctx, "w", store.StatusOK); len(ok) != 2 {
		all, _ := st.Documents(ctx, "w", "")
		t.Fatalf("indexing failed: %+v", all)
	}

	eng, err := New(g, st, llm, askyourdocs.GenerateConfig(llmSpec, askyourdocs.GenerateOptions{NumCtx: 8192, KeepAlive: "10m"}))
	if err != nil {
		t.Fatal(err)
	}
	q := "Wie lange ist meine Kündigungsfrist?"
	res, err := eng.Search(ctx, q, "w")
	if err != nil || res.VectorErr != nil {
		t.Fatalf("search: %v / %v", err, res.VectorErr)
	}
	if len(res.Results) == 0 || !strings.Contains(res.Results[0].Hit.Text, "Kündigungsfrist") {
		t.Fatalf("top hit wrong: %+v", res.Results)
	}

	var text strings.Builder
	chunks := 0
	ans, err := eng.Answer(ctx, q, res.Results, func(s string) { text.WriteString(s); chunks++ })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("answer (%d chunks, confidence %s, %d of %d sources used): %s", chunks, ans.Confidence, len(ans.Used), ans.Total, text.String())
	if !strings.Contains(strings.ToLower(text.String()), "drei monate") {
		t.Errorf("answer misses the fact")
	}
	if !regexp.MustCompile(`\[\d+\]`).MatchString(text.String()) {
		t.Errorf("answer has no citation marker")
	}
	if chunks < 2 {
		t.Errorf("answer was not streamed")
	}

	// An English question about German documents is answered in English.
	qEN := "How long is my notice period?"
	resEN, err := eng.Search(ctx, qEN, "w")
	if err != nil || len(resEN.Results) == 0 {
		t.Fatalf("search (en): %v, %d results", err, len(resEN.Results))
	}
	var textEN strings.Builder
	if _, err := eng.Answer(ctx, qEN, resEN.Results, func(s string) { textEN.WriteString(s) }); err != nil {
		t.Fatal(err)
	}
	t.Logf("answer (en): %s", textEN.String())
	if low := strings.ToLower(textEN.String()); !strings.Contains(low, "three months") && !strings.Contains(low, "3 months") {
		t.Errorf("English question not answered in English")
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
