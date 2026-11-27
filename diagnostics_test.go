package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dbis-ilm/personalrag/internal/config"
	"github.com/dbis-ilm/personalrag/internal/logging"
)

// TestDiagnostics runs offline: logging in a temp data dir, a crash left by
// a "previous run", no models.
func TestDiagnostics(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0o700)
	home, _ := os.UserHomeDir()
	os.WriteFile(filepath.Join(dataDir, "logs", "crash.log"),
		[]byte("panic: runtime error\n\ngoroutine 7 [running]:\n"+home+"/x/main.go:12\n"), 0o600)

	logs, err := logging.Setup(dataDir, "info")
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	s := NewAppService(dataDir, logs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer s.ServiceShutdown()
	slog.Info("marker-for-tail", "path", filepath.Join(home, "Dokumente", "a.pdf"))

	if st := s.Status(); st.LastCrash == "" {
		t.Errorf("previous crash not in status: %+v", st)
	}
	d := s.Diagnostics()
	for _, want := range []string{"PersonalRAG " + version, "System: ", "Schema: index v3, history v1", "Index: 0 Sammlungen", "--- Absturz am", "panic: runtime error", "marker-for-tail", "~/Dokumente/a.pdf"} {
		if !strings.Contains(d, want) {
			t.Errorf("diagnostics lack %q", want)
		}
	}
	if home != "" && strings.Contains(d, home) {
		t.Error("home directory not redacted")
	}

	if err := s.SetLogLevel("verbose"); err == nil {
		t.Error("invalid level accepted")
	}
	if err := s.SetLogLevel("debug"); err != nil || s.LogLevel() != "debug" {
		t.Fatalf("SetLogLevel: %v, now %q", err, s.LogLevel())
	}
	if cfg, _ := config.Load(dataDir); cfg.LogLevel != "debug" {
		t.Errorf("level not persisted: %+v", cfg)
	}
	s.SetLogLevel("info")
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "", 420 * time.Millisecond: "0,4 s", 12 * time.Second: "12 s", 125 * time.Second: "2:05 min"} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
