// Package watcher reports file system changes below a set of folders, so a
// changed document is re-indexed within seconds instead of at the next
// periodic scan.
//
// macOS uses FSEvents, which watches whole trees with one stream; kqueue
// (fsnotify's macOS backend) would need a file descriptor per file and does
// not carry large folders. Windows and Linux use fsnotify with one watch per
// directory.
//
// Events are only a trigger: the watcher hands over paths, never what
// happened to them, and the caller looks at the file system itself. Raw
// events are debounced (an editor's save is a burst of create, write and
// rename) and delivered in batches. When the platform reports lost events,
// the batch asks for a full rescan instead.
package watcher

import (
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Batch is one debounced set of changes.
type Batch struct {
	Paths  []string // changed files or folders, sorted, no duplicates
	Rescan bool     // events were lost: compare everything with the index
}

// Options tune the debouncing.
type Options struct {
	// Quiet is how long no event must arrive before a batch is delivered.
	Quiet time.Duration
	// MaxWait caps the delay under a constant stream of events.
	MaxWait time.Duration
	// Skip, if set, drops paths early (e.g. files that are never indexed).
	Skip func(path string) bool
	// SkipDir, if set, names folders not to watch at all (hidden folders,
	// node_modules); only the per-directory backends use it.
	SkipDir func(name string) bool
}

// DefaultOptions deliver a change about a second after it settles, and
// after three seconds at the latest.
var DefaultOptions = Options{Quiet: time.Second, MaxWait: 3 * time.Second}

// backend is the platform-specific event source.
type backend interface {
	setRoots(roots []string) error
	close() error
}

// Watcher watches a set of root folders.
type Watcher struct {
	opts    Options
	onBatch func(Batch)
	be      backend

	mu      sync.Mutex
	roots   []string
	pending map[string]bool
	rescan  bool
	first   time.Time // first event of the pending batch
	timer   *time.Timer
	closed  bool
}

// New starts a watcher without roots; onBatch runs on its own goroutine,
// one batch at a time.
func New(opts Options, onBatch func(Batch)) (*Watcher, error) {
	if opts.Quiet <= 0 {
		opts.Quiet = DefaultOptions.Quiet
	}
	if opts.MaxWait < opts.Quiet {
		opts.MaxWait = max(DefaultOptions.MaxWait, opts.Quiet)
	}
	w := &Watcher{opts: opts, onBatch: onBatch, pending: map[string]bool{}}
	be, err := newBackend(w.event, opts.SkipDir)
	if err != nil {
		return nil, err
	}
	w.be = be
	return w, nil
}

// SetRoots replaces the watched folders. Unchanged sets are a no-op, so
// callers may call it after every scan.
func (w *Watcher) SetRoots(roots []string) error {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		clean = append(clean, filepath.Clean(r))
	}
	sort.Strings(clean)
	w.mu.Lock()
	if w.closed || equal(clean, w.roots) {
		w.mu.Unlock()
		return nil
	}
	w.roots = clean
	w.mu.Unlock()
	return w.be.setRoots(clean)
}

// Close stops watching; a pending batch is dropped.
func (w *Watcher) Close() error {
	w.mu.Lock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	return w.be.close()
}

// event records one raw event; path "" with lost=true means events were
// dropped somewhere.
func (w *Watcher) event(path string, lost bool) {
	if path != "" && !lost && w.opts.Skip != nil && w.opts.Skip(path) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	now := time.Now()
	if len(w.pending) == 0 && !w.rescan {
		w.first = now
	}
	if lost {
		w.rescan = true
	}
	if path != "" {
		w.pending[path] = true
	}
	wait := w.opts.Quiet
	if left := w.first.Add(w.opts.MaxWait).Sub(now); left < wait {
		wait = max(left, 0)
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(wait, w.flush)
	} else {
		w.timer.Reset(wait)
	}
}

func (w *Watcher) flush() {
	w.mu.Lock()
	if w.closed || (len(w.pending) == 0 && !w.rescan) {
		w.mu.Unlock()
		return
	}
	b := Batch{Paths: make([]string, 0, len(w.pending)), Rescan: w.rescan}
	for p := range w.pending {
		b.Paths = append(b.Paths, p)
	}
	w.pending, w.rescan = map[string]bool{}, false
	w.mu.Unlock()
	sort.Strings(b.Paths)
	w.onBatch(b)
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
