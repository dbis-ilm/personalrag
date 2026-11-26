// Package logging sets up the app's log: a size-rotated text file in the
// data directory (also mirrored to stderr), a level that can change at
// runtime, and a crash file that catches panics the process dies of.
//
// Privacy rule for everyone logging here: questions, answers and document
// text only at debug. File paths may appear in warnings and errors, where
// they are needed to diagnose a problem.
package logging

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/firebase/genkit/go/core/logger"
)

const (
	fileName  = "personalrag.log"
	crashName = "crash.log"
	maxSize   = 5 << 20 // bytes per log file before rotating
	keep      = 3       // rotated files kept: .1 … .3
)

// Logs is the installed logging setup.
type Logs struct {
	Dir     string
	file    string
	level   *slog.LevelVar
	w       *rotatingWriter
	handler slog.Handler

	mu    sync.Mutex
	crash *crashReport // left by the previous run, until taken
}

type crashReport struct {
	when time.Time
	text string
}

// ParseLevel accepts debug, info, warn and error; empty means info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unbekannte Protokollstufe %q (debug, info, warn, error)", s)
}

// Options vary Setup for processes other than the app window.
type Options struct {
	// FileName within <dataDir>/logs; empty means personalrag.log.
	FileName string
	// NoCrashFile leaves crash.log alone: only the app reports crashes.
	NoCrashFile bool
}

// Setup installs the process-wide handler writing to <dataDir>/logs. Call it
// before genkit.Init so Genkit's records go to the same place.
func Setup(dataDir, level string) (*Logs, error) {
	return SetupWith(dataDir, level, Options{})
}

// SetupWith is Setup with options, e.g. a separate file for the MCP server,
// which runs as its own process next to the app.
func SetupWith(dataDir, level string, opts Options) (*Logs, error) {
	name := opts.FileName
	if name == "" {
		name = fileName
	}
	lv, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w, err := openRotating(filepath.Join(dir, name), maxSize, keep)
	if err != nil {
		return nil, err
	}
	l := &Logs{Dir: dir, file: name, level: new(slog.LevelVar), w: w}
	l.level.Set(lv)

	h := slog.NewTextHandler(io.MultiWriter(w, os.Stderr), &slog.HandlerOptions{Level: l.level})
	l.handler = h
	logger.SetDefaultHandler(h)             // Genkit filters through this handler's level
	slog.SetLogLoggerLevel(slog.LevelError) // the standard log package lands at error

	if opts.NoCrashFile {
		return l, nil
	}

	// A crash file from the previous run is moved aside before the runtime
	// opens it for this run (Windows cannot rename a file that is open).
	l.crash = archiveCrash(dir)

	// Fatal panics bypass slog; the runtime writes them here as well.
	if f, err := os.OpenFile(filepath.Join(dir, crashName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			slog.Warn("crash output not set", "err", err)
		}
		f.Close()
	}
	return l, nil
}

// SetLevel changes the level at runtime.
func (l *Logs) SetLevel(level string) error {
	lv, err := ParseLevel(level)
	if err != nil {
		return err
	}
	l.level.Set(lv)
	return nil
}

// Logger returns a logger on the same file that drops records below min,
// whatever the app's level: for chatty components such as Wails' asset
// server, which logs every request at info.
func (l *Logs) Logger(min slog.Level) *slog.Logger {
	return slog.New(minLevel{Handler: l.handler, min: min})
}

type minLevel struct {
	slog.Handler
	min slog.Level
}

func (m minLevel) Enabled(ctx context.Context, lv slog.Level) bool {
	return lv >= m.min && m.Handler.Enabled(ctx, lv)
}

func (m minLevel) WithAttrs(a []slog.Attr) slog.Handler {
	return minLevel{Handler: m.Handler.WithAttrs(a), min: m.min}
}

func (m minLevel) WithGroup(name string) slog.Handler {
	return minLevel{Handler: m.Handler.WithGroup(name), min: m.min}
}

// Level returns the current level as a word.
func (l *Logs) Level() string {
	return strings.ToLower(l.level.Level().String())
}

// Close flushes and closes the log file.
func (l *Logs) Close() error { return l.w.Close() }

// Tail returns up to n last lines of the log, older rotated file included
// when the current one is short.
func (l *Logs) Tail(n int) []string {
	cur := filepath.Join(l.Dir, l.file)
	lines := readLines(cur)
	if len(lines) < n {
		lines = append(readLines(cur+".1"), lines...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// TakeCrash returns the report of a crash from the previous run, once.
func (l *Logs) TakeCrash() (time.Time, string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.crash == nil {
		return time.Time{}, "", false
	}
	c := l.crash
	l.crash = nil
	return c.when, c.text, true
}

// archiveCrash moves a non-empty crash file to crash-<time>.log and returns
// its last 80 lines.
func archiveCrash(dir string) *crashReport {
	path := filepath.Join(dir, crashName)
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return nil
	}
	lines := readLines(path)
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	archived := filepath.Join(dir, "crash-"+fi.ModTime().Format("20060102-150405")+".log")
	if err := os.Rename(path, archived); err != nil {
		return nil
	}
	return &crashReport{when: fi.ModTime(), text: strings.Join(lines, "\n")}
}

func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

// rotatingWriter appends to a file and renames it to .1 (shifting older
// ones up to .keep) once it would exceed max bytes.
type rotatingWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func openRotating(path string, max int64, keep int) (*rotatingWriter, error) {
	w := &rotatingWriter{path: path, max: max, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, fi.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, errors.New("log closed")
	}
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	for i := w.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return w.open()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
