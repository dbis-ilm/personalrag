package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The demo collections are unpacked into <data>/demo, added once, offer
// their example questions, and lose their files when removed. Runs offline.
func TestAddDemo(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	os.MkdirAll(data, 0o700)
	s := NewAppService(data, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer s.ServiceShutdown()

	n, err := s.AddDemo()
	if err != nil || n != len(demoSets) {
		t.Fatalf("AddDemo = %d, %v", n, err)
	}
	if n, err := s.AddDemo(); err != nil || n != 0 {
		t.Fatalf("second AddDemo = %d, %v", n, err)
	}
	colls, err := s.Collections()
	if err != nil || len(colls) != len(demoSets) {
		t.Fatalf("collections: %+v, %v", colls, err)
	}
	// Every embedded file is unpacked, the thesis in its subfolder.
	want := 0
	fs.WalkDir(demoFS, "demo", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Dir(p) != "demo" {
			want++
		}
		return nil
	})
	got := 0
	filepath.WalkDir(filepath.Join(data, "demo"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			got++
		}
		return nil
	})
	if got != want || want < 50 {
		t.Errorf("unpacked %d files, embedded %d", got, want)
	}
	if _, err := os.Stat(filepath.Join(data, "demo", "pruefung", "arbeit", "bachelorarbeit-jana-meier.docx")); err != nil {
		t.Error(err)
	}

	qs, err := s.DemoQuestions("demo-skript")
	if err != nil || len(qs) != 2 || qs[0].Mode != "agent" || qs[0].CollectionID != "demo-skript" {
		t.Fatalf("questions for demo-skript: %+v, %v", qs, err)
	}
	all, _ := s.DemoQuestions("")
	if len(all) < 10 {
		t.Errorf("questions for all collections: %d", len(all))
	}

	if err := s.RemoveCollection("demo-skript"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "demo", "skript")); !os.IsNotExist(err) {
		t.Errorf("demo files kept: %v", err)
	}
	if qs, _ := s.DemoQuestions("demo-skript"); len(qs) != 0 {
		t.Errorf("questions for a removed collection: %+v", qs)
	}
	// Adding again restores only the missing collection.
	if n, err := s.AddDemo(); err != nil || n != 1 {
		t.Fatalf("AddDemo after removal = %d, %v", n, err)
	}
}

// Removing a collection whose ID merely looks like a demo never touches
// anything outside <data>/demo.
func TestRemoveDemoFilesStaysInside(t *testing.T) {
	data := t.TempDir()
	s := &AppService{dataDir: data}
	outside := filepath.Join(data, "keep")
	os.MkdirAll(outside, 0o755)
	for _, id := range []string{"demo-../keep", "demo-", "keep", `demo-..\keep`} {
		s.removeDemoFiles(id)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("removed a folder outside demo/: %v", err)
	}
}
