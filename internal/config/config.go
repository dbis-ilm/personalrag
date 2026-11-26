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
	OllamaHost   string `json:"ollamaHost"`
	LLM          string `json:"llm"`       // model spec, e.g. "ollama/gemma4:12b"; empty = search only
	Embedding    string `json:"embedding"` // model spec, e.g. "ollama/bge-m3"
	EmbeddingDim int    `json:"embeddingDim"`
	SetupDone    bool   `json:"setupDone"`
	LogLevel     string `json:"logLevel,omitempty"` // debug | info | warn | error; empty = info
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
