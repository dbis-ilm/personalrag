package main

import (
	"testing"

	"github.com/dbis-ilm/personalrag/internal/config"
	"github.com/dbis-ilm/personalrag/internal/i18n"
)

func TestSetLanguage(t *testing.T) {
	s := startService(t)
	t.Cleanup(func() { i18n.Set(i18n.Resolve("")) })

	relabeled := 0
	s.onLanguageChange(func() { relabeled++ })
	if err := s.SetLanguage("en"); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.Language != "en" || st.LanguageSetting != "en" || relabeled != 1 {
		t.Fatalf("status %s/%s, relabeled %d", st.Language, st.LanguageSetting, relabeled)
	}
	if got := s.ClientConfigs()[2].Hint; got != "Run in a terminal." {
		t.Errorf("hint not English: %q", got)
	}
	if cfg, _ := config.Load(s.dataDir); cfg.Language != "en" {
		t.Errorf("setting not saved: %q", cfg.Language)
	}

	// "" follows the system; TestMain pins that to German.
	if err := s.SetLanguage(""); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Language != "de" || st.LanguageSetting != "" {
		t.Fatalf("system language: %s/%q", st.Language, st.LanguageSetting)
	}
	if err := s.SetLanguage("fr"); err == nil {
		t.Error("unsupported language accepted")
	}
}

func TestConfidenceCode(t *testing.T) {
	for in, want := range map[string]string{"hoch": "high", "mittel": "medium", "niedrig": "low", "high": "high", "": ""} {
		if got := confidenceCode(in); got != want {
			t.Errorf("confidenceCode(%q) = %q", in, got)
		}
	}
}
