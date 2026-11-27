package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/config"
	"github.com/dbis-ilm/personalrag/internal/i18n"
	"github.com/dbis-ilm/personalrag/internal/ingest"
	"github.com/dbis-ilm/personalrag/internal/secrets"
)

// Model providers: Ollama on this machine, and an OpenAI-compatible server
// such as GPUStack reached by URL and API key. Models are named by spec,
// "ollama/<name>" or "gpustack/<name>", as askyourdocs resolves them.

const (
	providerOllama   = askyourdocs.ProviderOllama
	providerGPUStack = askyourdocs.ProviderGPUStack
)

// ProviderInfo is one provider's reachability and models.
type ProviderInfo struct {
	ID         string      `json:"id"` // "ollama" | "gpustack"
	Label      string      `json:"label"`
	URL        string      `json:"url"`
	Configured bool        `json:"configured"`
	OK         bool        `json:"ok"`
	Error      string      `json:"error"`
	Remote     bool        `json:"remote"` // not on this machine: excerpts leave it
	Host       string      `json:"host"`
	KeySet     bool        `json:"keySet"`
	Models     []ModelInfo `json:"models"`
}

type ModelInfo struct {
	Spec      string `json:"spec"`
	Name      string `json:"name"`
	Size      string `json:"size"`
	Embedding bool   `json:"embedding"` // known or guessed embedding model
}

type RuntimeInfo struct {
	Providers          []ProviderInfo `json:"providers"`
	LLM                string         `json:"llm"`       // configured spec
	Embedding          string         `json:"embedding"` // configured spec
	SuggestedLLM       string         `json:"suggestedLlm"`
	SuggestedEmbedding string         `json:"suggestedEmbedding"`
	RemoteConsent      string         `json:"remoteConsent"` // host the user agreed to send excerpts to
}

type ConnectionSettings struct {
	OllamaHost     string `json:"ollamaHost"`
	GPUStackURL    string `json:"gpustackUrl"`
	GPUStackKeySet bool   `json:"gpustackKeySet"`
}

type ExclusionSettings struct {
	Patterns []string `json:"patterns"`
	Builtin  []string `json:"builtin"`
}

// errRemoteConsent asks the UI for explicit consent before excerpts go to
// another machine.
var errRemoteConsent = consentError{}

// consentError reads in the current language when shown, while errors.Is
// still recognises it.
type consentError struct{}

func (consentError) Error() string { return i18n.T("conn.consentRequired") }

// remoteHost returns the host that receives document excerpts under cfg, or
// "" when every configured model runs on this machine.
func remoteHost(cfg config.Settings) string {
	usesGPUStack := providerOf(cfg.LLM) == providerGPUStack || providerOf(cfg.Embedding) == providerGPUStack
	if !usesGPUStack || cfg.GPUStackURL == "" || askyourdocs.IsLocalURL(cfg.GPUStackURL) {
		return ""
	}
	return askyourdocs.URLHost(cfg.GPUStackURL)
}

// checkConsent fails when excerpts would go to a host the user has not
// approved, e.g. after the GPUStack address was changed by hand.
func checkConsent(cfg config.Settings) error {
	if h := remoteHost(cfg); h != "" && cfg.RemoteConsent != h {
		return i18n.Errorf("core.consentMissing", h)
	}
	return nil
}

func providerOf(spec string) string {
	if spec == "" {
		return ""
	}
	p, _ := askyourdocs.ParseModelSpec(spec)
	return p
}

func providerLabel(id string) string {
	if id == providerGPUStack {
		return "GPUStack"
	}
	return "Ollama"
}

// ---- Bound methods ----

func (s *AppService) Connections() ConnectionSettings {
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	key, _ := secrets.Get(secrets.GPUStackKey)
	return ConnectionSettings{OllamaHost: cfg.OllamaHost, GPUStackURL: cfg.GPUStackURL, GPUStackKeySet: key != ""}
}

// TestConnection checks a provider with the given address (and key) without
// saving. An empty GPUStack key falls back to the stored one.
func (s *AppService) TestConnection(provider, url, key string) ProviderInfo {
	base, err := validateURL(url)
	if err != nil {
		return ProviderInfo{ID: provider, Label: providerLabel(provider), URL: url, Configured: true, Error: err.Error()}
	}
	if provider == providerGPUStack && key == "" {
		key, _ = secrets.Get(secrets.GPUStackKey)
	}
	ctx, cancel := context.WithTimeout(s.appCtx, 8*time.Second)
	defer cancel()
	if provider == providerGPUStack {
		return probeGPUStack(ctx, base, key)
	}
	return probeOllama(ctx, base)
}

// RuntimeInfo checks the configured providers and lists their models.
func (s *AppService) RuntimeInfo() RuntimeInfo {
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(s.appCtx, 8*time.Second)
	defer cancel()

	info := RuntimeInfo{LLM: cfg.LLM, Embedding: cfg.Embedding, RemoteConsent: cfg.RemoteConsent}
	ollama := probeOllama(ctx, cfg.OllamaHost)
	gpu := ProviderInfo{ID: providerGPUStack, Label: "GPUStack", Models: []ModelInfo{}}
	if cfg.GPUStackURL != "" {
		key, _ := secrets.Get(secrets.GPUStackKey)
		gpu = probeGPUStack(ctx, cfg.GPUStackURL, key)
	}
	info.Providers = []ProviderInfo{ollama, gpu}

	// Suggestions prefer local models.
	var llms, embs []string
	for _, p := range info.Providers {
		for _, m := range p.Models {
			if m.Embedding {
				embs = append(embs, m.Spec)
			} else {
				llms = append(llms, m.Spec)
			}
		}
	}
	info.SuggestedEmbedding = prefer(embs, "ollama/bge-m3", "ollama/nomic-embed-text", "ollama/mxbai-embed-large")
	info.SuggestedLLM = prefer(llms, "ollama/gemma4:12b", "ollama/qwen3:8b", "ollama/gemma3:12b", "ollama/llama3.1:8b")
	return info
}

// validateURL checks an entered server address; refusals are worded in the
// UI language.
func validateURL(raw string) (string, error) {
	base, err := askyourdocs.ValidateBaseURL(raw)
	switch {
	case errors.Is(err, askyourdocs.ErrCredentialsInURL):
		return "", i18n.Errorf("conn.credentialsInURL")
	case err != nil:
		return "", i18n.Errorf("conn.badURL", strings.TrimSpace(raw))
	}
	return base, nil
}

// serverErrorText words a failed call to a model server in the UI language.
func serverErrorText(err error) string {
	var se *askyourdocs.ServerError
	if !errors.As(err, &se) {
		return err.Error()
	}
	switch se.Kind {
	case askyourdocs.ErrUnreachable:
		return i18n.T("conn.unreachable")
	case askyourdocs.ErrAPIKeyRejected:
		return i18n.T("conn.keyRejected")
	case askyourdocs.ErrNotEmbeddingModel:
		return i18n.T("conn.notEmbedding", se.Model)
	case askyourdocs.ErrNoEmbedding:
		return i18n.T("conn.noVector", se.Model)
	}
	return i18n.T("conn.serverStatus", se.Status)
}

func probeOllama(ctx context.Context, base string) ProviderInfo {
	p := ProviderInfo{ID: providerOllama, Label: "Ollama", URL: base, Configured: true, Remote: !askyourdocs.IsLocalURL(base), Host: askyourdocs.URLHost(base), Models: []ModelInfo{}}
	models, err := askyourdocs.ListOllamaModels(ctx, base)
	if err != nil {
		p.Error = i18n.T("conn.ollamaUnreachable", base)
		return p
	}
	p.OK = true
	for _, m := range models {
		p.Models = append(p.Models, ModelInfo{Spec: providerOllama + "/" + m.Name, Name: m.Name, Size: fmt.Sprintf("%.1f GB", float64(m.Size)/1e9), Embedding: m.Embedding})
	}
	return p
}

func probeGPUStack(ctx context.Context, base, key string) ProviderInfo {
	p := ProviderInfo{ID: providerGPUStack, Label: "GPUStack", URL: base, Configured: true, Remote: !askyourdocs.IsLocalURL(base), Host: askyourdocs.URLHost(base), KeySet: key != "", Models: []ModelInfo{}}
	names, err := askyourdocs.ListOpenAIModels(ctx, base, key)
	if err != nil {
		p.Error = i18n.T("conn.gpustackError", base, serverErrorText(err))
		return p
	}
	p.OK = true
	for _, n := range names {
		p.Models = append(p.Models, ModelInfo{Spec: providerGPUStack + "/" + n, Name: n, Embedding: askyourdocs.LooksLikeEmbeddingModel(n)})
	}
	return p
}

func prefer(specs []string, wanted ...string) string {
	for _, w := range wanted {
		for _, n := range specs {
			if n == w || strings.TrimSuffix(n, ":latest") == w {
				return n
			}
		}
	}
	if len(specs) > 0 {
		return specs[0]
	}
	return ""
}

// SaveConnections stores the Ollama address and the GPUStack server. The API
// key goes to the system keychain, never to settings.json; an empty key
// keeps the stored one, removeKey deletes it. An empty GPUStack URL turns
// GPUStack off.
func (s *AppService) SaveConnections(ollamaHost, gpustackURL, gpustackKey string, removeKey bool) error {
	oh, err := validateURL(ollamaHost)
	if err != nil {
		return i18n.Errorf("conn.ollama", err)
	}
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()

	gu := strings.TrimSpace(gpustackURL)
	if gu == "" {
		if providerOf(cfg.LLM) == providerGPUStack || providerOf(cfg.Embedding) == providerGPUStack {
			return i18n.Errorf("conn.gpustackInUse")
		}
		removeKey = true
	} else if gu, err = validateURL(gu); err != nil {
		return i18n.Errorf("conn.gpustack", err)
	}

	switch {
	case removeKey:
		if err := secrets.Delete(secrets.GPUStackKey); err != nil {
			return i18n.Errorf("core.keychain", err)
		}
	case gpustackKey != "":
		if err := secrets.Set(secrets.GPUStackKey, strings.TrimSpace(gpustackKey)); err != nil {
			return i18n.Errorf("core.keychain", err)
		}
	}

	cfg.OllamaHost, cfg.GPUStackURL = oh, gu
	if err := s.saveSettings(cfg); err != nil {
		return err
	}
	s.restartAsync()
	return nil
}

// SaveModels sets the answer model (may be empty: search only) and the
// embedding model by spec and restarts the core. A model on another machine
// needs confirmRemote once per host. Changing the embedding model
// re-indexes everything.
func (s *AppService) SaveModels(llmSpec, embeddingSpec string, confirmRemote bool) error {
	if embeddingSpec == "" {
		return i18n.Errorf("conn.embeddingRequired")
	}
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	for _, spec := range []string{llmSpec, embeddingSpec} {
		switch providerOf(spec) {
		case "", providerOllama:
		case providerGPUStack:
			if cfg.GPUStackURL == "" {
				return i18n.Errorf("conn.gpustackMissing")
			}
		default:
			return i18n.Errorf("conn.providerUnknown", spec)
		}
	}
	cand := cfg
	cand.LLM, cand.Embedding = llmSpec, embeddingSpec
	if h := remoteHost(cand); h != "" && cand.RemoteConsent != h {
		if !confirmRemote {
			return errRemoteConsent
		}
		cand.RemoteConsent = h
	}

	ctx, cancel := context.WithTimeout(s.appCtx, 90*time.Second)
	defer cancel()
	_, name := askyourdocs.ParseModelSpec(embeddingSpec)
	var dim int
	var err error
	if providerOf(embeddingSpec) == providerGPUStack {
		key, _ := secrets.Get(secrets.GPUStackKey)
		dim, err = askyourdocs.OpenAIEmbeddingDim(ctx, cfg.GPUStackURL, key, name)
	} else {
		dim, err = askyourdocs.OllamaEmbeddingDim(ctx, cfg.OllamaHost, name)
	}
	switch {
	case errors.Is(err, askyourdocs.ErrNotEmbeddingModel), errors.Is(err, askyourdocs.ErrNoEmbedding):
		return errors.New(serverErrorText(err))
	case err != nil:
		return i18n.Errorf("conn.embeddingFailed", name, serverErrorText(err))
	}
	cand.EmbeddingDim = dim
	if err := s.saveSettings(cand); err != nil {
		return err
	}
	s.restartAsync()
	return nil
}

func builtinExclusions() []string {
	return []string{i18n.T("exclusions.hidden"), i18n.T("exclusions.dev"), i18n.T("exclusions.office"), i18n.T("exclusions.size")}
}

func (s *AppService) Exclusions() ExclusionSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ExclusionSettings{Patterns: slices.Clone(s.settings.Exclude), Builtin: builtinExclusions()}
}

// SaveExclusions stores name patterns to skip and re-scans, which removes
// documents that now match.
func (s *AppService) SaveExclusions(patterns []string) error {
	var clean []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(clean, p) {
			continue
		}
		if err := ingest.ValidatePattern(p); err != nil {
			return err
		}
		clean = append(clean, p)
	}
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	cfg.Exclude = clean
	if err := s.saveSettings(cfg); err != nil {
		return err
	}
	if c, err := s.current(); err == nil && c.ix != nil {
		c.ix.SetExcludes(clean)
		go func() {
			if err := c.ix.ScanAll(s.appCtx); err != nil {
				s.emit(EventNotice, Notice{Text: i18n.T("exclusions.scanFailed", err.Error()), Kind: "warn"})
			}
		}()
	}
	return nil
}
