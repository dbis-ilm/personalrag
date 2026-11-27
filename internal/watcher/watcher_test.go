package watcher

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector records batches and lets a test wait for a path.
type collector struct {
	mu      sync.Mutex
	batches []Batch
	notify  chan struct{}
}

func newCollector() *collector { return &collector{notify: make(chan struct{}, 100)} }

func (c *collector) add(b Batch) {
	c.mu.Lock()
	c.batches = append(c.batches, b)
	c.mu.Unlock()
	c.notify <- struct{}{}
}

func (c *collector) reset() {
	c.mu.Lock()
	c.batches = nil
	c.mu.Unlock()
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.batches)
}

func (c *collector) seen(p string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range c.batches {
		if slices.Contains(b.Paths, p) {
			return true
		}
	}
	return false
}

func (c *collector) waitFor(t *testing.T, p string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for !c.seen(p) {
		select {
		case <-c.notify:
		case <-deadline:
			c.mu.Lock()
			defer c.mu.Unlock()
			t.Fatalf("no event for %s within %s; batches: %+v", p, within, c.batches)
		}
	}
}

// tempRoot returns a canonical temp folder (FSEvents reports /private/var,
// not the /var symlink).
func tempRoot(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var fast = Options{Quiet: 200 * time.Millisecond, MaxWait: time.Second, SkipDir: func(n string) bool { return strings.HasPrefix(n, ".") }}

func start(t *testing.T, roots ...string) (*Watcher, *collector) {
	t.Helper()
	c := newCollector()
	w, err := New(fast, c.add)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := w.SetRoots(roots); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // FSEvents needs a moment before the stream reports
	return w, c
}

func TestCreateModifyRemove(t *testing.T) {
	root := tempRoot(t)
	_, c := start(t, root)

	f := filepath.Join(root, "Vertrag.md")
	os.WriteFile(f, []byte("eins"), 0o644)
	c.waitFor(t, f, 5*time.Second)

	c.reset()
	os.WriteFile(f, []byte("zwei"), 0o644)
	c.waitFor(t, f, 5*time.Second)

	c.reset()
	os.Remove(f)
	c.waitFor(t, f, 5*time.Second)
}

// A file in a folder created after the watch started must be seen; the
// per-directory backends have to add the new folder themselves.
func TestNewSubfolder(t *testing.T) {
	root := tempRoot(t)
	_, c := start(t, root)

	sub := filepath.Join(root, "2026", "Protokolle")
	os.MkdirAll(sub, 0o755)
	c.waitFor(t, filepath.Join(root, "2026"), 5*time.Second)
	time.Sleep(300 * time.Millisecond) // let the backend add the new folder
	f := filepath.Join(sub, "Sitzung.md")
	os.WriteFile(f, []byte("x"), 0o644)
	c.waitFor(t, f, 5*time.Second)
}

// A burst of writes arrives as few batches, not one per write.
func TestDebounce(t *testing.T) {
	root := tempRoot(t)
	_, c := start(t, root)

	f := filepath.Join(root, "a.txt")
	for i := range 30 {
		os.WriteFile(f, []byte(strings.Repeat("x", i)), 0o644)
		time.Sleep(10 * time.Millisecond)
	}
	c.waitFor(t, f, 5*time.Second)
	time.Sleep(500 * time.Millisecond)
	if n := c.count(); n > 3 {
		t.Errorf("30 writes gave %d batches", n)
	}
}

func TestSetRootsAndClose(t *testing.T) {
	a, b := tempRoot(t), tempRoot(t)
	w, c := start(t, a)

	if err := w.SetRoots([]string{b}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	os.WriteFile(filepath.Join(a, "alt.txt"), []byte("x"), 0o644)
	fb := filepath.Join(b, "neu.txt")
	os.WriteFile(fb, []byte("x"), 0o644)
	c.waitFor(t, fb, 5*time.Second)
	time.Sleep(500 * time.Millisecond)
	if c.seen(filepath.Join(a, "alt.txt")) {
		t.Error("old root still watched")
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	c.reset()
	os.WriteFile(filepath.Join(b, "nach-close.txt"), []byte("x"), 0o644)
	time.Sleep(time.Second)
	if n := c.count(); n != 0 {
		t.Errorf("%d batches after Close", n)
	}
}

func TestHiddenFoldersNotWatched(t *testing.T) {
	root := tempRoot(t)
	os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755)
	w, _ := start(t, root)
	if fb, ok := w.be.(interface{ watchedDirs() []string }); ok {
		for _, d := range fb.watchedDirs() {
			if strings.Contains(d, ".git") {
				t.Errorf("hidden folder watched: %s", d)
			}
		}
	}
}
