// Package config holds the user's settings and the per-user data directory.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const AppName = "PersonalRAG"

// Settings are persisted as settings.json in the data directory.
type Settings struct {
	OllamaHost string `json:"ollamaHost"`
	// OllamaOff: the user left the Ollama address empty and uses another
	// provider only. OllamaHost keeps the last address for turning it on.
	OllamaOff    bool   `json:"ollamaOff,omitempty"`
	LLM          string `json:"llm"`       // model spec, e.g. "ollama/gemma4:12b"; empty = search only
	Embedding    string `json:"embedding"` // model spec, e.g. "ollama/bge-m3"
	EmbeddingDim int    `json:"embeddingDim"`
	// AgentModel is the model spec for the agent mode, which plans and calls
	// tools; it needs a model with tool calling. Empty = the answer model.
	AgentModel string `json:"agentModel,omitempty"`
	SetupDone  bool   `json:"setupDone"`
	LogLevel   string `json:"logLevel,omitempty"` // debug | info | warn | error; empty = info

	// GPUStackURL is an OpenAI-compatible API base (GPUStack, LM Studio,
	// llama.cpp), e.g. https://gpu.example/v1. Its API key lives in the
	// system keychain, not here.
	GPUStackURL string `json:"gpustackUrl,omitempty"`
	// RemoteConsent is the host the user agreed to send document excerpts
	// to; models on any other non-local host are refused.
	RemoteConsent string `json:"remoteConsent,omitempty"`
	// Language of the UI: "de", "en", or "" to follow the system.
	Language string `json:"language,omitempty"`
	// Exclude are file and folder name patterns skipped when indexing.
	Exclude []string `json:"exclude,omitempty"`
}

func Defaults() Settings {
	return Settings{OllamaHost: "http://localhost:11434"}
}

// DataDir is ~/Library/Application Support/PersonalRAG on macOS and
// %APPDATA%\PersonalRAG on Windows. PERSONALRAG_DATA overrides it.
func DataDir() (string, error) {
	if d := os.Getenv("PERSONALRAG_DATA"); d != "" {
		return d, os.MkdirAll(d, 0o700)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, AppName)
	return d, os.MkdirAll(d, 0o700)
}

func Load(dir string) (Settings, error) {
	s := Defaults()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Defaults(), err
	}
	if s.OllamaHost == "" {
		s.OllamaHost = Defaults().OllamaHost
	}
	return s, nil
}

// Save writes settings atomically.
func Save(dir string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}
