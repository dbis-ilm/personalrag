//go:build !darwin || !cgo

package watcher

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// fsnotifyBackend watches every directory below the roots (inotify and
// ReadDirectoryChangesW are per directory) and adds new ones as they appear.
type fsnotifyBackend struct {
	emit    func(path string, lost bool)
	skipDir func(name string) bool
	w       *fsnotify.Watcher

	mu      sync.Mutex
	roots   []string
	watched map[string]bool
}

func newBackend(emit func(string, bool), skipDir func(string) bool) (backend, error) {
	if skipDir == nil {
		skipDir = func(string) bool { return false }
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	b := &fsnotifyBackend{emit: emit, skipDir: skipDir, w: w, watched: map[string]bool{}}
	go b.loop()
	return b, nil
}

func (b *fsnotifyBackend) loop() {
	for {
		select {
		case e, ok := <-b.w.Events:
			if !ok {
				return
			}
			if e.Has(fsnotify.Create) {
				if info, err := os.Lstat(e.Name); err == nil && info.IsDir() {
					b.addTree(e.Name) // files created inside before the watch existed are covered by the folder event
				}
			}
			if e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename) {
				b.forget(e.Name)
			}
			b.emit(e.Name, false)
		case err, ok := <-b.w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				b.emit("", true)
			} else {
				slog.Warn("watcher error", "err", err)
			}
		}
	}
}

func (b *fsnotifyBackend) setRoots(roots []string) error {
	b.mu.Lock()
	old := b.watched
	b.roots, b.watched = roots, map[string]bool{}
	b.mu.Unlock()
	for dir := range old {
		_ = b.w.Remove(dir)
	}
	var errs []error
	for _, r := range roots {
		if err := b.addTree(r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// addTree watches dir and every directory below it, except folders skipDir
// names.
func (b *fsnotifyBackend) addTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir && b.skipDir(d.Name()) {
			return fs.SkipDir
		}
		b.mu.Lock()
		seen := b.watched[p]
		b.watched[p] = true
		b.mu.Unlock()
		if seen {
			return nil
		}
		if err := b.w.AddWith(p, fsnotify.WithBufferSize(windowsBufferSize)); err != nil {
			slog.Warn("watcher: cannot watch folder", "err", err)
			b.emit("", true) // fall back to a scan for what we cannot watch
		}
		return nil
	})
}

// forget drops watches for a removed or renamed folder and its subfolders
// (inotify drops them itself; the map must follow).
func (b *fsnotifyBackend) forget(p string) {
	prefix := p + string(filepath.Separator)
	b.mu.Lock()
	defer b.mu.Unlock()
	for dir := range b.watched {
		if dir == p || strings.HasPrefix(dir, prefix) {
			delete(b.watched, dir)
			_ = b.w.Remove(dir)
		}
	}
}

func (b *fsnotifyBackend) close() error { return b.w.Close() }

// windowsBufferSize is the ReadDirectoryChangesW buffer per folder (ignored
// elsewhere). fsnotify's 64 KB default would cost 320 MB for 5,000 folders;
// 8 KB holds about a hundred events between reads, and an overflow falls
// back to a full scan.
const windowsBufferSize = 8 * 1024

// watchedDirs lists the folders with a watch (for tests).
func (b *fsnotifyBackend) watchedDirs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.watched))
	for d := range b.watched {
		out = append(out, d)
	}
	return out
}
