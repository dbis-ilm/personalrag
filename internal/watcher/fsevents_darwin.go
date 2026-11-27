//go:build darwin && cgo

package watcher

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsevents"
)

// fseventsBackend runs one FSEvents stream over all roots. FSEvents
// watches whole trees, so new subfolders need no extra work.
type fseventsBackend struct {
	emit     func(path string, lost bool)
	mu       sync.Mutex
	stream   *fsevents.EventStream
	stopping *atomic.Bool
	done     chan struct{}
}

func newBackend(emit func(string, bool), _ func(string) bool) (backend, error) {
	return &fseventsBackend{emit: emit}, nil
}

func (b *fseventsBackend) setRoots(roots []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopLocked()
	if len(roots) == 0 {
		return nil
	}
	es := &fsevents.EventStream{
		Paths:   roots,
		Latency: 300 * time.Millisecond,
		Flags:   fsevents.FileEvents | fsevents.WatchRoot,
		Events:  make(chan []fsevents.Event, 16),
	}
	if err := es.Start(); err != nil {
		return err
	}
	stopping, done := new(atomic.Bool), make(chan struct{})
	b.stream, b.stopping, b.done = es, stopping, done
	go func() {
		for {
			select {
			case batch := <-es.Events:
				if stopping.Load() {
					continue // keep draining so the callback never blocks during Stop
				}
				for _, e := range batch {
					lost := e.Flags&(fsevents.MustScanSubDirs|fsevents.KernelDropped|fsevents.UserDropped|fsevents.RootChanged) != 0
					p := e.Path
					if p != "" && !strings.HasPrefix(p, "/") {
						p = "/" + p // FSEvents reports paths relative to the volume root
					}
					b.emit(p, lost)
				}
			case <-done:
				return
			}
		}
	}()
	return nil
}

// stopLocked stops the stream. fsevents does not close its channel, so the
// reader drains until Stop has returned and is then told to quit.
func (b *fseventsBackend) stopLocked() {
	if b.stream == nil {
		return
	}
	b.stopping.Store(true)
	b.stream.Stop()
	close(b.done)
	b.stream, b.stopping, b.done = nil, nil, nil
}

func (b *fseventsBackend) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopLocked()
	return nil
}
