package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOpenAI is an OpenAI-compatible server with one chat and one
// embedding model behind a bearer key.
func fakeOpenAI(t *testing.T, key string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			w.Write([]byte(`{"object":"list","data":[{"id":"qwen3-8b"},{"id":"bge-m3"}]}`))
		}
	})
	mux.HandleFunc("POST /v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var req struct{ Model string }
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "bge-m3" {
			http.Error(w, "model does not support embeddings", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3,0.4]}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGPUStackClient(t *testing.T) {
	ctx := context.Background()
	srv := fakeOpenAI(t, "sk-test")
	base := srv.URL + "/v1"

	models, err := GPUStackModels(ctx, base, "sk-test")
	if err != nil || strings.Join(models, ",") != "qwen3-8b,bge-m3" {
		t.Fatalf("models = %v, %v", models, err)
	}
	if _, err := GPUStackModels(ctx, base, "wrong"); err == nil || !strings.Contains(err.Error(), "API-Schlüssel abgelehnt") {
		t.Fatalf("wrong key: %v", err)
	}
	if dim, err := GPUStackEmbeddingDim(ctx, base, "sk-test", "bge-m3"); err != nil || dim != 4 {
		t.Fatalf("dim = %d, %v", dim, err)
	}
	if _, err := GPUStackEmbeddingDim(ctx, base, "sk-test", "qwen3-8b"); err == nil || !strings.Contains(err.Error(), "kein Embedding-Modell") {
		t.Fatalf("chat model as embedder: %v", err)
	}
}

func TestBaseURLHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"https://gpu.example/v1/":  "https://gpu.example/v1",
		" http://localhost:11434 ": "http://localhost:11434",
	} {
		if got, err := ValidateBaseURL(in); err != nil || got != want {
			t.Errorf("ValidateBaseURL(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"gpu.example", "ftp://x", "https://", "https://user:pw@gpu.example/v1"} {
		if _, err := ValidateBaseURL(bad); err == nil {
			t.Errorf("ValidateBaseURL(%q) accepted", bad)
		}
	}
	for base, local := range map[string]bool{
		"http://localhost:11434": true, "http://127.0.0.1:8080/v1": true, "http://[::1]:80": true,
		"https://gpu.example/v1": false, "http://192.168.1.20/v1": false,
	} {
		if IsLocal(base) != local {
			t.Errorf("IsLocal(%q) = %v", base, !local)
		}
	}
	if !LooksLikeEmbedding("bge-m3") || !LooksLikeEmbedding("nomic-embed-text") || LooksLikeEmbedding("qwen3-8b") || LooksLikeEmbedding("bge-reranker-v2") {
		t.Error("LooksLikeEmbedding heuristics wrong")
	}
}
