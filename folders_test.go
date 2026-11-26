package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestAddFolderOverlap runs offline: no models are configured, so the
// service only manages collections.
func TestAddFolderOverlap(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "Dokumente")
	inner := filepath.Join(docs, "Verträge")
	other := filepath.Join(base, "Notizen")
	for _, d := range []string{inner, other} {
		os.MkdirAll(d, 0o755)
	}
	link := filepath.Join(base, "link-zu-vertraegen")
	haveLink := os.Symlink(inner, link) == nil

	s := NewAppService(filepath.Join(base, "data"))
	os.MkdirAll(filepath.Join(base, "data"), 0o700)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer s.ServiceShutdown()

	if _, err := s.AddFolderPath(inner); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, wantErr string
	}{
		{"same folder", inner, "gehört bereits"},
		{"same folder, unclean", inner + string(filepath.Separator) + "." + string(filepath.Separator), "gehört bereits"},
		{"subfolder", filepath.Join(inner, "sub"), "ist kein Ordner"}, // does not exist
		{"parent folder", docs, "enthält"},
	}
	if haveLink {
		cases = append(cases, struct{ name, path, wantErr string }{"symlink to same folder", link, "gehört bereits"})
	}
	for _, c := range cases {
		if _, err := s.AddFolderPath(c.path); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
	os.MkdirAll(filepath.Join(inner, "sub"), 0o755)
	if _, err := s.AddFolderPath(filepath.Join(inner, "sub")); err == nil || !strings.Contains(err.Error(), "gehört bereits") {
		t.Errorf("existing subfolder: err = %v", err)
	}
	if _, err := s.AddFolderPath(other); err != nil {
		t.Errorf("unrelated folder refused: %v", err)
	}
	if c, _ := s.Collections(); len(c) != 2 {
		t.Fatalf("want 2 collections, got %+v", c)
	}
}
