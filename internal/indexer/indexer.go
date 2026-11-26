// Package indexer keeps the index in sync with the collections' folders: a
// scan compares files with the index (size/mtime first, hash on change) and
// queues new or changed files; one worker indexes them, so indexing never
// competes with itself for the embedding model.
package indexer

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dbis-ilm/askyourdocs"
	"github.com/firebase/genkit/go/core"
	"github.com/firebase/genkit/go/genkit"

	"personalrag/internal/ingest"
	"personalrag/internal/store"
)

const (
	chunkSize    = 1200
	chunkOverlap = 150
)

// Progress is what the UI shows about the worker.
type Progress struct {
	Active     bool   `json:"active"`
	Paused     bool   `json:"paused"`
	Collection string `json:"collection"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Current    string `json:"current"`
}

type job struct {
	path         string
	collectionID string
}

// fileInput / fileResult are the indexFile flow's input and output.
type fileInput struct {
	Path         string `json:"path"`
	DocumentID   int64  `json:"documentId"`
	CollectionID string `json:"collectionId"`
}

type fileResult struct {
	Chunks int  `json:"chunks"`
	OCR    bool `json:"ocr"`
}

type Indexer struct {
	st         *store.Store
	flow       *core.Flow[fileInput, fileResult, struct{}]
	onProgress func(Progress)
	onChange   func()

	mu       sync.Mutex
	queue    []job
	queued   map[string]bool
	paused   bool
	done     int
	current  job
	busy     bool
	wake     chan struct{}
	collName map[string]string
	scanning sync.Mutex
	excludes []string // user patterns, matched against file and folder names

}

// New creates an indexer and defines its "indexFile" flow on g (askyourdocs
// steps run inside a flow). onProgress and onChange may be nil; onChange is
// called whenever documents were added, updated or removed.
func New(g *genkit.Genkit, st *store.Store, onProgress func(Progress), onChange func()) *Indexer {
	ix := &Indexer{
		st: st, onProgress: onProgress, onChange: onChange,
		queued: map[string]bool{}, wake: make(chan struct{}, 1), collName: map[string]string{},
	}
	ix.flow = genkit.DefineFlow(g, "indexFile", func(ctx context.Context, in fileInput) (fileResult, error) {
		paras, err := ingest.Extract(in.Path)
		if errors.Is(err, ingest.ErrNeedsOCR) {
			_, err := st.DeleteChunks(ctx, in.DocumentID)
			return fileResult{OCR: true}, err
		}
		if err != nil {
			return fileResult{}, err
		}
		src := &ingest.FileSource{Path: in.Path, DocumentID: in.DocumentID, CollectionID: in.CollectionID, Paragraphs: paras}
		n, _, err := askyourdocs.IndexSource(ctx, st, src, chunkSize, chunkOverlap, nil)
		return fileResult{Chunks: n}, err
	})
	return ix
}

// Run processes the queue until ctx ends.
func (ix *Indexer) Run(ctx context.Context) {
	for {
		j, ok := ix.next()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-ix.wake:
				continue
			}
		}
		ix.process(ctx, j)
		ix.mu.Lock()
		ix.busy = false
		ix.done++
		if len(ix.queue) == 0 {
			ix.done = 0
		}
		ix.mu.Unlock()
		ix.emit()
		if ix.onChange != nil {
			ix.onChange()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (ix *Indexer) next() (job, bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.paused || len(ix.queue) == 0 {
		return job{}, false
	}
	j := ix.queue[0]
	ix.queue = ix.queue[1:]
	delete(ix.queued, j.path)
	ix.current, ix.busy = j, true
	return j, true
}

func (ix *Indexer) signal() {
	select {
	case ix.wake <- struct{}{}:
	default:
	}
}

// SetExcludes sets name patterns (filepath.Match syntax, e.g. "*.tmp",
// "Archiv") that skip matching files and folders on the next scan; already
// indexed documents that now match are removed then.
func (ix *Indexer) SetExcludes(patterns []string) {
	ix.mu.Lock()
	ix.excludes = append([]string(nil), patterns...)
	ix.mu.Unlock()
}

func (ix *Indexer) excluded(name string) bool {
	ix.mu.Lock()
	pats := ix.excludes
	ix.mu.Unlock()
	return ingest.MatchesAny(name, pats)
}

// Enqueue adds files; front puts them ahead of the background backlog
// (files the user just dropped).
func (ix *Indexer) Enqueue(paths []string, collectionID string, front bool) {
	ix.mu.Lock()
	var jobs []job
	for _, p := range paths {
		if ix.queued[p] {
			continue
		}
		ix.queued[p] = true
		jobs = append(jobs, job{path: p, collectionID: collectionID})
	}
	if front {
		ix.queue = append(jobs, ix.queue...)
	} else {
		ix.queue = append(ix.queue, jobs...)
	}
	ix.mu.Unlock()
	ix.emit()
	ix.signal()
}

func (ix *Indexer) SetPaused(p bool) {
	ix.mu.Lock()
	ix.paused = p
	ix.mu.Unlock()
	ix.emit()
	ix.signal()
}

func (ix *Indexer) Progress() Progress {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.progressLocked()
}

func (ix *Indexer) progressLocked() Progress {
	p := Progress{Paused: ix.paused, Done: ix.done, Total: ix.done + len(ix.queue)}
	if ix.busy {
		p.Total++
		p.Active = true
		p.Current = filepath.Base(ix.current.path)
		p.Collection = ix.collName[ix.current.collectionID]
	} else if len(ix.queue) > 0 {
		p.Active = true
		p.Collection = ix.collName[ix.queue[0].collectionID]
	}
	return p
}

func (ix *Indexer) emit() {
	if ix.onProgress != nil {
		ix.onProgress(ix.Progress())
	}
}

// ScanAll scans every collection's folders.
func (ix *Indexer) ScanAll(ctx context.Context) error {
	colls, err := ix.st.Collections(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range colls {
		if err := ix.ScanCollection(ctx, c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ScanCollection compares a collection's folders with the index, queues new
// and changed files and removes documents whose files are gone.
func (ix *Indexer) ScanCollection(ctx context.Context, c store.Collection) error {
	ix.scanning.Lock()
	defer ix.scanning.Unlock()
	ix.mu.Lock()
	ix.collName[c.ID] = c.Name
	ix.mu.Unlock()

	known, err := ix.st.Documents(ctx, c.ID, "")
	if err != nil {
		return err
	}
	byPath := make(map[string]store.Document, len(known))
	for _, d := range known {
		byPath[d.Path] = d
	}
	seen := map[string]bool{}
	var todo []string
	changed := false

	for _, root := range c.Folders {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				slog.Warn("scan: skipping", "path", path, "err", err)
				if d != nil && d.IsDir() && path != root {
					return fs.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				if path != root && (ingest.SkipDir(d.Name()) || ix.excluded(d.Name())) {
					return fs.SkipDir
				}
				return nil
			}
			if ingest.SkipFile(d.Name()) || !ingest.Supported(path) || ix.excluded(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > ingest.MaxFileSize {
				return nil
			}
			seen[path] = true
			doc, ok := byPath[path]
			switch {
			case !ok:
				if _, err := ix.st.UpsertDocument(ctx, store.Document{
					Path: path, CollectionID: c.ID, Size: info.Size(), ModTime: info.ModTime().Unix(),
					Format: ingest.Format(path), Status: store.StatusPending,
				}); err != nil {
					return err
				}
				changed = true
				todo = append(todo, path)
			case doc.Status == store.StatusPending || doc.Status == store.StatusRunning:
				todo = append(todo, path)
			case doc.Size != info.Size() || doc.ModTime != info.ModTime().Unix():
				h, err := ingest.HashFile(path)
				if err == nil && h == doc.Hash && doc.Status == store.StatusOK {
					_ = ix.st.TouchDocument(ctx, doc.ID, info.Size(), info.ModTime().Unix())
					return nil
				}
				todo = append(todo, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	removed := 0
	for path, d := range byPath {
		if !seen[path] {
			if err := ix.st.DeleteDocument(ctx, d.ID); err != nil {
				return err
			}
			changed = true
			removed++
		}
	}
	if changed && ix.onChange != nil {
		ix.onChange()
	}
	// Counts only: file names stay out of the log at info.
	slog.Info("scan done", "collection", c.ID, "files", len(seen), "queued", len(todo), "removed", removed)
	ix.Enqueue(todo, c.ID, false)
	return nil
}

// process indexes one file and records the outcome on its document row.
func (ix *Indexer) process(ctx context.Context, j job) {
	ix.emit()
	info, err := os.Stat(j.path)
	if err != nil {
		if d, ok, _ := ix.st.DocumentByPath(ctx, j.path); ok {
			_ = ix.st.DeleteDocument(ctx, d.ID)
		}
		return
	}
	id, err := ix.st.UpsertDocument(ctx, store.Document{
		Path: j.path, CollectionID: j.collectionID, Size: info.Size(), ModTime: info.ModTime().Unix(),
		Format: ingest.Format(j.path), Status: store.StatusRunning,
	})
	if err != nil {
		slog.Error("index: upsert failed", "path", j.path, "err", err)
		return
	}
	if ix.onChange != nil {
		ix.onChange()
	}

	hash, err := ingest.HashFile(j.path)
	if err != nil {
		_ = ix.st.SetDocumentState(ctx, id, store.StatusError, "Datei nicht lesbar", "")
		return
	}
	res, err := ix.flow.Run(ctx, fileInput{Path: j.path, DocumentID: id, CollectionID: j.collectionID})
	switch {
	case err == nil && res.OCR:
		_ = ix.st.SetDocumentState(ctx, id, store.StatusOCR, "Keine Textebene erkannt", "")
		return
	case err != nil && (ctx.Err() != nil || isUnreachable(err)):
		// Leave it pending; the next scan retries.
		_ = ix.st.SetDocumentState(ctx, id, store.StatusPending, "Embedding-Modell nicht erreichbar", "")
		return
	case err != nil:
		_ = ix.st.SetDocumentState(ctx, id, store.StatusError, describe(err), "")
		return
	}
	n := res.Chunks
	note := ""
	if n == 0 {
		note = "Kein Text gefunden"
	}
	_ = ix.st.SetDocumentState(ctx, id, store.StatusOK, note, hash)
	slog.Debug("indexed", "path", j.path, "chunks", n)
}

func isUnreachable(err error) bool {
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "no such host") || strings.Contains(s, "deadline exceeded")
}

func describe(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(strings.ToLower(s), "encrypt") || strings.Contains(strings.ToLower(s), "password"):
		return "Datei ist passwortgeschützt"
	case strings.Contains(s, "not a PDF") || strings.Contains(s, "invalid header") || strings.Contains(s, "zip: not a valid"):
		return "Datei ist beschädigt oder hat ein ungültiges Format"
	case len(s) > 140:
		return s[:140] + "…"
	}
	return s
}
