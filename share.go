package main

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dbis-ilm/personalrag/internal/i18n"
)

// maxMailtoLen keeps a mailto URL within what Windows' shell hands to the
// mail program (about 2,000 characters); macOS and Linux take more.
const maxMailtoLen = 1900

// ShareMail opens the default mail program with a new message. The body
// is shortened, with a note, when the link would get too long; truncated
// reports that, so the UI can point to copying or saving instead.
func (s *AppService) ShareMail(subject, body string) (truncated bool, err error) {
	u, truncated := mailtoURL(subject, body, i18n.T("share.mailTruncated"), maxMailtoLen)
	app := application.Get()
	if app == nil {
		return truncated, errors.New("no application")
	}
	return truncated, app.Browser.OpenURL(u)
}

// mailtoURL builds a mailto link, cutting the body (at a paragraph or line
// break where possible) until the link fits max characters.
func mailtoURL(subject, body, note string, max int) (string, bool) {
	enc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	build := func(b string) string { return "mailto:?subject=" + enc(subject) + "&body=" + enc(b) }
	u := build(body)
	if len(u) <= max {
		return u, false
	}
	r := []rune(body)
	for n := len(r); n > 0; n = n * 9 / 10 {
		cut := string(r[:n])
		if i := strings.LastIndex(cut, "\n"); i > n/2 {
			cut = cut[:i]
		}
		if u = build(strings.TrimSpace(cut) + "\n\n[…] " + note); len(u) <= max {
			return u, true
		}
	}
	return build(note), true
}

// Print prints the main window; the page shows only its print area then.
// The print dialog also offers saving as PDF.
func (s *AppService) Print() error {
	app := application.Get()
	if app == nil {
		return errors.New("no application")
	}
	w, ok := app.Window.GetByName("main")
	if !ok {
		return errors.New("main window not found")
	}
	return w.Print()
}

// SaveExport asks where to save content (Markdown) and writes it there.
// It returns the chosen path, or "" when the dialog was cancelled.
func (s *AppService) SaveExport(suggestedName, content string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("no application")
	}
	name := sanitizeFileName(suggestedName)
	path, err := app.Dialog.SaveFile().
		SetFilename(name+".md").
		AddFilter("Markdown", "*.md").
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += ".md"
	}
	return path, os.WriteFile(path, []byte(content), 0o644)
}

// sanitizeFileName turns a question into a file name: no path separators
// or characters Windows forbids, at most 60 characters.
func sanitizeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:60]))
	}
	s = strings.Trim(s, ". ")
	if s == "" {
		return "PersonalRAG"
	}
	return s
}
