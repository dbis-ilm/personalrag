// Package i18n holds the app's language and the Go-side texts shown to the
// user (errors, notices, tray menu, dialogs) in German and English. The UI
// translates its own texts; Go sends codes where it can and sentences from
// this catalogue where it must. Texts for agents (MCP) are always English
// and do not go through here.
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// Lang is a supported language.
type Lang string

const (
	DE Lang = "de"
	EN Lang = "en"
)

var current atomic.Value // Lang

func init() { current.Store(EN) }

// Parse maps a setting or locale ("de", "de-DE", "de_AT.UTF-8", "en-US") to
// a supported language; anything not German is English.
func Parse(s string) Lang {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "de" || strings.HasPrefix(s, "de-") || strings.HasPrefix(s, "de_") || strings.HasPrefix(s, "de.") {
		return DE
	}
	return EN
}

// Resolve turns a setting into the language to use: "de" or "en" as given,
// anything else ("" = follow the system) from PERSONALRAG_LANG or the
// operating system.
func Resolve(setting string) Lang {
	switch strings.ToLower(setting) {
	case "de":
		return DE
	case "en":
		return EN
	}
	if env := os.Getenv("PERSONALRAG_LANG"); env != "" {
		return Parse(env)
	}
	return Parse(systemLocale())
}

// Set makes l the current language.
func Set(l Lang) { current.Store(l) }

// Current returns the current language.
func Current() Lang { return current.Load().(Lang) }

// T returns the text for key in the current language, formatted with args
// like fmt.Sprintf. A missing key returns the key itself, so a gap shows up
// instead of crashing.
func T(key string, args ...any) string {
	return TIn(Current(), key, args...)
}

// TIn is T for a given language.
func TIn(l Lang, key string, args ...any) string {
	e, ok := catalog[key]
	if !ok {
		return key
	}
	s := e.en
	if l == DE {
		s = e.de
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Errorf is fmt.Errorf with a catalogue text: the result wraps an error
// passed with %w, as fmt.Errorf does.
func Errorf(key string, args ...any) error {
	e, ok := catalog[key]
	if !ok {
		return fmt.Errorf("%s", key)
	}
	s := e.en
	if Current() == DE {
		s = e.de
	}
	return fmt.Errorf(s, args...)
}
