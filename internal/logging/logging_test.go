package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	w, err := openRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(w, "line %02d %s\n", i, strings.Repeat("x", 30)) // 41 bytes
	}
	w.Close()
	for _, p := range []string{path, path + ".1", path + ".2"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s missing: %v", p, err)
		}
		if fi.Size() > 100 {
			t.Errorf("%s has %d bytes, limit 100", p, fi.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("more rotated files kept than configured")
	}
	if last := readLines(path); len(last) == 0 || !strings.HasPrefix(last[len(last)-1], "line 19") {
		t.Errorf("newest line not in current file: %q", last)
	}
}

// Setup changes the process-wide logger, so the tests using it run in order
// in one function.
func TestSetupLevelTailCrash(t *testing.T) {
	dir := t.TempDir()
	// A crash file left by a previous run.
	os.MkdirAll(filepath.Join(dir, "logs"), 0o700)
	os.WriteFile(filepath.Join(dir, "logs", crashName), []byte("panic: boom\n\ngoroutine 1 [running]:\nmain.main()\n"), 0o600)
	l, err := Setup(dir, "info")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	slog.Debug("hidden-at-info", "question", "Wie hoch ist mein Gehalt?")
	slog.Info("visible-at-info")
	if err := l.SetLevel("debug"); err != nil {
		t.Fatal(err)
	}
	slog.Debug("visible-at-debug")
	if l.Level() != "debug" {
		t.Errorf("Level() = %q", l.Level())
	}
	if err := l.SetLevel("loud"); err == nil {
		t.Error("unknown level accepted")
	}

	wails := l.Logger(slog.LevelWarn)
	wails.Info("quiet-component-info")
	wails.Warn("quiet-component-warn")

	tail := strings.Join(l.Tail(50), "\n")
	if strings.Contains(tail, "quiet-component-info") || !strings.Contains(tail, "quiet-component-warn") {
		t.Error("minimum level of component logger not applied")
	}
	if strings.Contains(tail, "hidden-at-info") || strings.Contains(tail, "Gehalt") {
		t.Error("debug record leaked at info level")
	}
	if !strings.Contains(tail, "visible-at-info") || !strings.Contains(tail, "visible-at-debug") {
		t.Errorf("expected records missing:\n%s", tail)
	}
	if got := l.Tail(1); len(got) != 1 || !strings.Contains(got[0], "quiet-component-warn") {
		t.Errorf("Tail(1) = %q", got)
	}

	// The previous run's crash is reported once, and the file moved aside.
	when, report, ok := l.TakeCrash()
	if !ok || !strings.Contains(report, "panic: boom") || time.Since(when) > time.Minute {
		t.Fatalf("TakeCrash = %v %q %v", when, report, ok)
	}
	if _, _, ok := l.TakeCrash(); ok {
		t.Error("crash reported twice")
	}
	if fi, err := os.Stat(filepath.Join(l.Dir, crashName)); err != nil || fi.Size() != 0 {
		t.Errorf("fresh crash file expected for this run: %v", err)
	}
	archived, _ := filepath.Glob(filepath.Join(l.Dir, "crash-*.log"))
	if len(archived) != 1 {
		t.Errorf("archived crash files: %v", archived)
	}
}
