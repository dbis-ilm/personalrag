package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dbis-ilm/personalrag/internal/config"
	"github.com/dbis-ilm/personalrag/internal/secrets"
)

// fakeGPUStack is an OpenAI-compatible server on 127.0.0.1 with a chat and
// an embedding model behind a bearer key.
func fakeGPUStack(t *testing.T, key string) string {
	t.Helper()
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		if ok(w, r) {
			w.Write([]byte(`{"data":[{"id":"qwen3-8b"},{"id":"bge-m3"}]}`))
		}
	})
	mux.HandleFunc("POST /v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if !ok(w, r) {
			return
		}
		var req struct{ Model string }
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "bge-m3" {
			http.Error(w, "not an embedding model", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3,0.4]}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func startService(t *testing.T) *AppService {
	t.Helper()
	dataDir := t.TempDir()
	s := NewAppService(dataDir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ServiceShutdown() })
	return s
}

func TestGPUStackConnectionAndModels(t *testing.T) {
	s := startService(t)
	base := fakeGPUStack(t, "sk-secret")

	if p := s.TestConnection("gpustack", base, "wrong"); p.OK || !strings.Contains(p.Error, "abgelehnt") {
		t.Fatalf("wrong key accepted: %+v", p)
	}
	if p := s.TestConnection("gpustack", base, "sk-secret"); !p.OK || len(p.Models) != 2 || p.Remote {
		t.Fatalf("test connection: %+v", p)
	}
	if err := s.SaveConnections("http://localhost:11434", base+"/", "sk-secret", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "core restart", 10*time.Second, func() bool { return s.Status().Ready })

	// The key is in the keychain, never in settings.json.
	if k, _ := secrets.Get(secrets.GPUStackKey); k != "sk-secret" {
		t.Fatalf("key not stored: %q", k)
	}
	cfg, _ := config.Load(s.dataDir)
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "sk-secret") || cfg.GPUStackURL != base {
		t.Fatalf("settings: %s", raw)
	}
	if c := s.Connections(); !c.GPUStackKeySet || c.GPUStackURL != base {
		t.Fatalf("connections: %+v", c)
	}
	// Testing again without typing the key uses the stored one.
	if p := s.TestConnection("gpustack", base, ""); !p.OK {
		t.Fatalf("stored key not used: %+v", p)
	}

	rt := s.RuntimeInfo()
	gpu := rt.Providers[1]
	if !gpu.OK || len(gpu.Models) != 2 || !gpu.Models[1].Embedding || gpu.Models[0].Spec != "gpustack/qwen3-8b" {
		t.Fatalf("runtime info: %+v", gpu)
	}

	if err := s.SaveModels("gpustack/qwen3-8b", "gpustack/qwen3-8b", false); err == nil || !strings.Contains(err.Error(), "kein Embedding-Modell") {
		t.Fatalf("chat model as embedder: %v", err)
	}
	if err := s.SaveModels("gpustack/qwen3-8b", "gpustack/bge-m3", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "core with GPUStack", 10*time.Second, func() bool {
		st := s.Status()
		return st.Ready && st.Embedding == "bge-m3" && st.LLMProvider == "GPUStack"
	})
	if cfg, _ := config.Load(s.dataDir); cfg.EmbeddingDim != 4 || cfg.RemoteConsent != "" {
		t.Fatalf("settings after models: %+v", cfg)
	}

	// GPUStack cannot be turned off while a model uses it.
	if err := s.SaveConnections("http://localhost:11434", "", "", false); err == nil {
		t.Fatal("removed GPUStack while in use")
	}
}

// A GPUStack server on another machine needs consent, once per host.
func TestRemoteConsent(t *testing.T) {
	s := startService(t)
	s.mu.Lock()
	s.settings.GPUStackURL = "https://gpu.example/v1"
	s.mu.Unlock()

	err := s.SaveModels("gpustack/qwen3-8b", "ollama/bge-m3", false)
	if !errors.Is(err, errRemoteConsent) {
		t.Fatalf("remote model without consent: %v", err)
	}

	cfg := config.Settings{GPUStackURL: "https://gpu.example/v1", LLM: "gpustack/qwen3-8b", Embedding: "ollama/bge-m3"}
	if remoteHost(cfg) != "gpu.example" || checkConsent(cfg) == nil {
		t.Fatal("consent not required for remote host")
	}
	cfg.RemoteConsent = "gpu.example"
	if checkConsent(cfg) != nil {
		t.Fatal("consent not accepted")
	}
	cfg.GPUStackURL = "https://other.example/v1" // address changed by hand
	if checkConsent(cfg) == nil {
		t.Fatal("consent carried over to another host")
	}
	cfg.GPUStackURL = "http://127.0.0.1:9000/v1"
	if remoteHost(cfg) != "" {
		t.Fatal("local server treated as remote")
	}
	cfg = config.Settings{GPUStackURL: "https://gpu.example/v1", LLM: "ollama/x", Embedding: "ollama/y"}
	if remoteHost(cfg) != "" {
		t.Fatal("configured but unused GPUStack treated as remote")
	}
}

func TestExclusions(t *testing.T) {
	s := startService(t)
	if err := s.SaveExclusions([]string{"Archiv/2019"}); err == nil {
		t.Fatal("path pattern accepted")
	}
	if err := s.SaveExclusions([]string{" *.bak ", "", "Archiv", "*.bak"}); err != nil {
		t.Fatal(err)
	}
	ex := s.Exclusions()
	if strings.Join(ex.Patterns, ",") != "*.bak,Archiv" || len(ex.Builtin) == 0 {
		t.Fatalf("exclusions: %+v", ex)
	}
	if cfg, _ := config.Load(s.dataDir); len(cfg.Exclude) != 2 {
		t.Fatalf("not persisted: %+v", cfg)
	}
}
