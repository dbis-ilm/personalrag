package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dbis-ilm/askyourdocs"

	"personalrag/internal/config"
	"personalrag/internal/history"
	"personalrag/internal/indexer"
	"personalrag/internal/ingest"
	"personalrag/internal/logging"
	"personalrag/internal/migrate"
	"personalrag/internal/paths"
	"personalrag/internal/rag"
	"personalrag/internal/store"
)

// Event names emitted to the frontend.
const (
	EventAnswerChunk    = "answer:chunk"
	EventAnswerDone     = "answer:done"
	EventIndexProgress  = "index:progress"
	EventSourcesChanged = "sources:changed"
	EventStatusChanged  = "status:changed"
	EventNotice         = "notice"
)

const rescanInterval = 15 * time.Minute

var collectionColors = []string{"#2452B8", "#B4651C", "#1F8A4C", "#7A4BC4", "#B23A6B", "#2C7A86", "#6B7079"}

// ---- Types shared with the frontend (TS types are generated) ----

type AppStatus struct {
	SetupDone  bool   `json:"setupDone"`
	Ready      bool   `json:"ready"`
	SearchOnly bool   `json:"searchOnly"` // no answer model
	LLM        string `json:"llm"`
	Embedding  string `json:"embedding"`
	Error      string `json:"error"`
	DataDir    string `json:"dataDir"`
	LastCrash  string `json:"lastCrash"` // when the previous run crashed; "" if it did not
}

type Collection struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Color            string   `json:"color"`
	DocCount         int      `json:"docCount"`
	SharedWithAgents bool     `json:"sharedWithAgents"`
	Folders          []string `json:"folders"`
}

// ConversationSummary is one entry of the persistent chat history.
type ConversationSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CollectionID string `json:"collectionId"`
	Updated      string `json:"updated"`
}

// Turn is one question with its sources and answer.
type Turn struct {
	ID         int64    `json:"id"`
	Question   string   `json:"question"`
	Standalone string   `json:"standalone"` // follow-up rewritten for search; "" if unchanged
	Mode       string   `json:"mode"`       // "answer" | "search"
	Sources    []Source `json:"sources"`
	Answer     string   `json:"answer"`
	Confidence string   `json:"confidence"`
	Note       string   `json:"note"`
	Error      string   `json:"error"`
	Done       bool     `json:"done"`
}

type ConversationDetail struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CollectionID string `json:"collectionId"`
	Turns        []Turn `json:"turns"`
}

// Source is one retrieved passage, with enough context for the preview.
type Source struct {
	N         int     `json:"n"`
	ChunkID   int64   `json:"chunkId"`
	File      string  `json:"file"`
	Dir       string  `json:"dir"`
	FullPath  string  `json:"fullPath"`
	Location  string  `json:"location"`
	Section   string  `json:"section"`
	Score     float64 `json:"score"` // 0–1, relative to the best hit
	Short     string  `json:"short"`
	Before    string  `json:"before"`
	Highlight string  `json:"highlight"`
	After     string  `json:"after"`
}

type SearchResult struct {
	ConversationID string   `json:"conversationId"`
	TurnID         int64    `json:"turnId"`
	Standalone     string   `json:"standalone"`
	Sources        []Source `json:"sources"`
	Millis         int      `json:"millis"`
	Notice         string   `json:"notice"`
}

type AnswerChunk struct {
	TurnID int64  `json:"turnId"`
	Text   string `json:"text"`
}

type AnswerDone struct {
	TurnID     int64  `json:"turnId"`
	Confidence string `json:"confidence"` // "hoch" | "mittel" | "niedrig"
	Note       string `json:"note"`
	Error      string `json:"error"`
}

type IndexProgress struct {
	Active     bool   `json:"active"`
	Paused     bool   `json:"paused"`
	Collection string `json:"collection"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Current    string `json:"current"`
}

type Folder struct {
	Path       string `json:"path"`
	Collection string `json:"collection"`
	Files      int    `json:"files"`
	State      string `json:"state"`
	StateKind  string `json:"stateKind"` // "ok" | "running" | "info"
}

type Document struct {
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	FullPath   string `json:"fullPath"`
	Collection string `json:"collection"`
	Format     string `json:"format"`
	Modified   string `json:"modified"`
	Status     string `json:"status"` // pending | running | ok | error | ocr | cloud
	Note       string `json:"note"`
	Chunks     int    `json:"chunks"`
}

type ModelInfo struct {
	Name      string `json:"name"`
	Size      string `json:"size"`
	Embedding bool   `json:"embedding"`
}

type RuntimeInfo struct {
	OllamaOK           bool        `json:"ollamaOk"`
	Host               string      `json:"host"`
	Error              string      `json:"error"`
	Models             []ModelInfo `json:"models"`
	LLM                string      `json:"llm"`       // currently configured (Ollama name)
	Embedding          string      `json:"embedding"` // currently configured (Ollama name)
	SuggestedLLM       string      `json:"suggestedLlm"`
	SuggestedEmbedding string      `json:"suggestedEmbedding"`
}

type Notice struct {
	Text string `json:"text"`
	Kind string `json:"kind"` // "ok" | "warn" | "error"
}

type SourcesChanged struct {
	At int64 `json:"at"`
}

type MCPStatus struct {
	Running   bool   `json:"running"`
	Address   string `json:"address"`
	TokenHint string `json:"tokenHint"`
	Note      string `json:"note"`
}

type ClientConfig struct {
	Name    string `json:"name"`
	Hint    string `json:"hint"`
	Snippet string `json:"snippet"`
}

type AccessLogEntry struct {
	Time   string `json:"time"`
	Client string `json:"client"`
	Tool   string `json:"tool"`
	Query  string `json:"query"`
	Result string `json:"result"`
	Kind   string `json:"kind"`
}

// ---- Service ----

// AppService is the app's API for the UI. It owns the index, the indexer
// and the query engine ("core"), which is rebuilt when models change.
type AppService struct {
	dataDir string
	appCtx  context.Context
	logs    *logging.Logs // nil in tests

	lastCrash string // label of the previous run's crash, if any
	crashText string

	mu       sync.RWMutex
	settings config.Settings
	core     *core
	status   AppStatus
	searches map[int64]cachedSearch
	order    []int64

	nextID   atomic.Int64
	debounce *time.Timer
	debMu    sync.Mutex
}

type cachedSearch struct {
	question string
	results  []rag.Result
}

type core struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	st     *store.Store
	ix     *indexer.Indexer // nil without an embedding model
	eng    *rag.Engine
	hist   *history.Store
}

// NewAppService creates the service for one data directory (see
// config.DataDir); main also derives the single-instance lock from it.
func NewAppService(dataDir string, logs *logging.Logs) *AppService {
	return &AppService{dataDir: dataDir, logs: logs, searches: map[int64]cachedSearch{}}
}

func (s *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.appCtx = ctx
	if s.logs != nil {
		if when, text, ok := s.logs.TakeCrash(); ok {
			s.lastCrash, s.crashText = when.Format("02.01.2006 15:04"), text
			slog.Warn("previous run crashed", "at", when)
		}
	}
	st, err := config.Load(s.dataDir)
	if err != nil {
		slog.Warn("settings unreadable, using defaults", "err", err)
	}
	s.settings = st
	s.restartCore()
	return nil
}

func (s *AppService) ServiceShutdown() error {
	s.mu.Lock()
	c := s.core
	s.core = nil
	s.mu.Unlock()
	if c != nil {
		c.stop()
	}
	return nil
}

func (c *core) stop() {
	c.cancel()
	c.wg.Wait()
	c.st.Close()
}

// restartCore (re)builds store, indexer and engine from the settings.
func (s *AppService) restartCore() {
	s.mu.Lock()
	old := s.core
	s.core = nil
	cfg := s.settings
	s.mu.Unlock()
	if old != nil {
		old.stop()
	}

	c, err := s.buildCore(cfg)
	status := AppStatus{
		SetupDone: cfg.SetupDone, DataDir: s.dataDir,
		LLM: modelName(cfg.LLM), Embedding: modelName(cfg.Embedding), SearchOnly: cfg.LLM == "",
		LastCrash: s.lastCrash,
	}
	if err != nil {
		slog.Error("core start failed", "err", err)
		status.Error = err.Error()
	} else {
		status.Ready = true
	}
	s.mu.Lock()
	s.core = c
	s.status = status
	s.searches = map[int64]cachedSearch{}
	s.order = nil
	s.mu.Unlock()
	s.emit(EventStatusChanged, status)
	s.sourcesChanged()
}

func (s *AppService) buildCore(cfg config.Settings) (*core, error) {
	ctx, cancel := context.WithCancel(s.appCtx)
	c := &core{cancel: cancel}
	fail := func(err error) (*core, error) {
		cancel()
		if c.st != nil {
			c.st.Close()
		}
		return nil, err
	}

	var specs []string
	if cfg.Embedding != "" {
		specs = append(specs, cfg.Embedding)
	}
	if cfg.LLM != "" {
		specs = append(specs, cfg.LLM)
	}
	providers, err := askyourdocs.NewProviders(
		askyourdocs.ProviderConfig{OllamaHost: cfg.OllamaHost, Timeout: 3 * time.Minute},
		[]string{askyourdocs.ProviderOllama, askyourdocs.ProviderGPUStack}, specs...)
	if err != nil {
		return fail(err)
	}
	g := genkit.Init(ctx, genkit.WithPlugins(providers.Plugins()...),
		genkit.WithPromptFS(rag.PromptFS), genkit.WithPromptDir("prompts"))

	var emb ai.Embedder
	if cfg.Embedding != "" && cfg.EmbeddingDim > 0 {
		if emb, err = providers.Embedder(g, cfg.Embedding, cfg.EmbeddingDim); err != nil {
			return fail(err)
		}
	}
	c.st, err = store.Open(filepath.Join(s.dataDir, "index.db"), emb, cfg.Embedding, cfg.EmbeddingDim)
	if errors.Is(err, store.ErrEmbeddingMismatch) {
		// The settings are authoritative: a new embedding model means the
		// old vectors are useless. Re-index everything.
		slog.Warn("embedding model changed, resetting vectors", "err", err)
		err = c.st.ResetVectors()
	}
	if err != nil {
		return fail(fmt.Errorf("Index öffnen: %w", err))
	}

	var llm ai.Model
	var llmCfg any
	if cfg.LLM != "" {
		if llm, err = providers.Model(g, cfg.LLM); err != nil {
			return fail(err)
		}
		llmCfg = askyourdocs.GenerateConfig(cfg.LLM, askyourdocs.GenerateOptions{NumCtx: 16384, KeepAlive: "30m"})
	}
	if c.eng, err = rag.New(g, c.st, llm, llmCfg); err != nil {
		return fail(err)
	}
	if c.hist, err = history.New(c.st.DB(), filepath.Join(s.dataDir, "index.db.history.bak")); err != nil {
		return fail(err)
	}
	if err := c.hist.MarkInterrupted(ctx); err != nil {
		slog.Warn("history: marking interrupted answers failed", "err", err)
	}

	if emb != nil {
		c.ix = indexer.New(g, c.st, func(p indexer.Progress) { s.emit(EventIndexProgress, toProgress(p)) }, s.sourcesChanged)
		c.wg.Add(2)
		go func() { defer c.wg.Done(); c.ix.Run(ctx) }()
		go func() {
			defer c.wg.Done()
			t := time.NewTicker(rescanInterval)
			defer t.Stop()
			for {
				if err := c.ix.ScanAll(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("scan failed", "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	return c, nil
}

func (s *AppService) current() (*core, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.core == nil {
		if s.status.Error != "" {
			return nil, errors.New(s.status.Error)
		}
		return nil, errors.New("Index ist noch nicht bereit")
	}
	return s.core, nil
}

func (s *AppService) emit(name string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data)
	}
}

// sourcesChanged tells the UI to reload lists, at most every 400 ms.
func (s *AppService) sourcesChanged() {
	s.debMu.Lock()
	defer s.debMu.Unlock()
	if s.debounce != nil {
		return
	}
	s.debounce = time.AfterFunc(400*time.Millisecond, func() {
		s.debMu.Lock()
		s.debounce = nil
		s.debMu.Unlock()
		s.emit(EventSourcesChanged, SourcesChanged{At: time.Now().UnixMilli()})
	})
}

// ---- Status & setup ----

func (s *AppService) Status() AppStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// RuntimeInfo checks Ollama and lists installed models with suggestions.
func (s *AppService) RuntimeInfo() RuntimeInfo {
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	info := RuntimeInfo{Host: cfg.OllamaHost, LLM: modelName(cfg.LLM), Embedding: modelName(cfg.Embedding), Models: []ModelInfo{}}
	ctx, cancel := context.WithTimeout(s.appCtx, 5*time.Second)
	defer cancel()
	models, err := rag.OllamaModels(ctx, cfg.OllamaHost)
	if err != nil {
		info.Error = "Ollama ist unter " + cfg.OllamaHost + " nicht erreichbar."
		return info
	}
	info.OllamaOK = true
	var llms, embs []string
	for _, m := range models {
		info.Models = append(info.Models, ModelInfo{Name: m.Name, Size: fmt.Sprintf("%.1f GB", float64(m.Size)/1e9), Embedding: m.Embedding})
		if m.Embedding {
			embs = append(embs, m.Name)
		} else {
			llms = append(llms, m.Name)
		}
	}
	info.SuggestedEmbedding = prefer(embs, "bge-m3", "nomic-embed-text", "mxbai-embed-large")
	info.SuggestedLLM = prefer(llms, "gemma4:12b", "qwen3:8b", "gemma3:12b", "llama3.1:8b")
	return info
}

func prefer(names []string, wanted ...string) string {
	for _, w := range wanted {
		for _, n := range names {
			if n == w || strings.TrimSuffix(n, ":latest") == w {
				return n
			}
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// SaveModels sets the Ollama models (names as Ollama lists them; llm may be
// empty for search-only) and restarts the core. Changing the embedding model
// re-indexes everything.
func (s *AppService) SaveModels(llm, embedding string) error {
	if embedding == "" {
		return errors.New("Ein Embedding-Modell ist erforderlich")
	}
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(s.appCtx, 10*time.Second)
	defer cancel()
	dim, err := rag.EmbeddingDim(ctx, cfg.OllamaHost, embedding)
	if err != nil {
		return fmt.Errorf("Embedding-Modell %s: %w", embedding, err)
	}
	cfg.Embedding, cfg.EmbeddingDim = "ollama/"+embedding, dim
	cfg.LLM = ""
	if llm != "" {
		cfg.LLM = "ollama/" + llm
	}
	if err := s.saveSettings(cfg); err != nil {
		return err
	}
	go s.restartCore()
	return nil
}

func (s *AppService) FinishSetup() error {
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	cfg.SetupDone = true
	if err := s.saveSettings(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.status.SetupDone = true
	st := s.status
	s.mu.Unlock()
	s.emit(EventStatusChanged, st)
	return nil
}

func (s *AppService) saveSettings(cfg config.Settings) error {
	if err := config.Save(s.dataDir, cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = cfg
	s.mu.Unlock()
	return nil
}

// ---- Collections & folders ----

func (s *AppService) Collections() ([]Collection, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	colls, err := c.st.Collections(s.appCtx)
	if err != nil {
		return nil, err
	}
	out := make([]Collection, len(colls))
	for i, cl := range colls {
		out[i] = Collection{ID: cl.ID, Name: cl.Name, Color: cl.Color, DocCount: cl.DocCount, SharedWithAgents: cl.SharedWithAgents, Folders: cl.Folders}
	}
	return out, nil
}

// AddFolder asks for a folder and turns it into a new collection. An empty
// ID in the result means the dialog was cancelled.
func (s *AppService) AddFolder() (Collection, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).CanChooseFiles(false).
		SetTitle("Ordner indexieren").SetButtonText("Hinzufügen").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return Collection{}, err
	}
	return s.AddFolderPath(path)
}

// AddFolderPath creates a collection for a folder and starts indexing it.
func (s *AppService) AddFolderPath(path string) (Collection, error) {
	c, err := s.current()
	if err != nil {
		return Collection{}, err
	}
	path = paths.Canonical(path)
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return Collection{}, fmt.Errorf("%s ist kein Ordner", path)
	}
	if err := s.checkOverlap(c, path); err != nil {
		return Collection{}, err
	}
	colls, _ := c.st.Collections(s.appCtx)
	name := filepath.Base(path)
	cl := store.Collection{
		ID:      uniqueID(slug(name), colls),
		Name:    name,
		Color:   collectionColors[len(colls)%len(collectionColors)],
		Folders: []string{path},
	}
	if err := c.st.CreateCollection(s.appCtx, cl); err != nil {
		return Collection{}, err
	}
	s.sourcesChanged()
	if c.ix != nil {
		go func() {
			if err := c.ix.ScanCollection(s.appCtx, cl); err != nil {
				slog.Warn("scan failed", "folder", path, "err", err)
			}
		}()
	}
	return Collection{ID: cl.ID, Name: cl.Name, Color: cl.Color, Folders: cl.Folders}, nil
}

// checkOverlap refuses a folder that is, contains or lies inside a watched
// folder: a file in two collections would switch between them on every scan.
func (s *AppService) checkOverlap(c *core, path string) error {
	folders, err := c.st.AllFolders(s.appCtx)
	if err != nil {
		return err
	}
	for _, f := range folders {
		switch {
		case paths.Within(path, f.Path):
			return fmt.Errorf("Der Ordner gehört bereits zur Sammlung „%s“", s.collectionName(f.CollectionID))
		case paths.Within(f.Path, path):
			return fmt.Errorf("Der Ordner enthält %s aus der Sammlung „%s“. Entferne diese Sammlung zuerst oder wähle einen anderen Ordner", displayPath(f.Path), s.collectionName(f.CollectionID))
		}
	}
	return nil
}

// AddPaths handles files and folders dropped on the window: folders become
// collections, files inside a watched folder are indexed first.
func (s *AppService) AddPaths(dropped []string) Notice {
	c, err := s.current()
	if err != nil {
		return Notice{Text: err.Error(), Kind: "error"}
	}
	var added, queued, outside, unsupported int
	var lastErr error
	for _, p := range dropped {
		p = paths.Canonical(p)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			if _, err := s.AddFolderPath(p); err != nil {
				lastErr = err
			} else {
				added++
			}
			continue
		}
		if !ingest.Supported(p) {
			unsupported++
			continue
		}
		id, ok, _ := c.st.CollectionForPath(s.appCtx, p)
		if !ok {
			outside++
			continue
		}
		if c.ix != nil {
			c.ix.Enqueue([]string{p}, id, true)
			queued++
		}
	}
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d Ordner hinzugefügt", added))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d Datei(en) werden zuerst indexiert", queued))
	}
	if outside > 0 {
		parts = append(parts, fmt.Sprintf("%d Datei(en) liegen in keinem überwachten Ordner, bitte den Ordner hinzufügen", outside))
	}
	if unsupported > 0 {
		parts = append(parts, fmt.Sprintf("%d Datei(en) im nicht unterstützten Format", unsupported))
	}
	if lastErr != nil {
		parts = append(parts, lastErr.Error())
	}
	kind := "ok"
	if outside+unsupported > 0 || lastErr != nil {
		kind = "warn"
	}
	n := Notice{Text: strings.Join(parts, " · "), Kind: kind}
	if n.Text != "" {
		s.emit(EventNotice, n)
	}
	return n
}

func (s *AppService) RemoveCollection(id string) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if err := c.st.DeleteCollection(s.appCtx, id); err != nil {
		return err
	}
	s.sourcesChanged()
	return nil
}

func (s *AppService) SetCollectionShared(id string, shared bool) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	return c.st.SetCollectionShared(s.appCtx, id, shared)
}

// Rescan checks all folders for changes now.
func (s *AppService) Rescan() error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if c.ix == nil {
		return errors.New("Kein Embedding-Modell eingerichtet")
	}
	go func() {
		if err := c.ix.ScanAll(s.appCtx); err != nil {
			slog.Warn("scan failed", "err", err)
		}
	}()
	return nil
}

func (s *AppService) Folders() ([]Folder, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	colls, err := c.st.Collections(s.appCtx)
	if err != nil {
		return nil, err
	}
	docs, err := c.st.Documents(s.appCtx, "", "")
	if err != nil {
		return nil, err
	}
	out := []Folder{}
	for _, cl := range colls {
		for _, f := range cl.Folders {
			counts := map[string]int{}
			total := 0
			for _, d := range docs {
				if d.CollectionID == cl.ID && strings.HasPrefix(d.Path, f+string(filepath.Separator)) {
					counts[d.Status]++
					total++
				}
			}
			fo := Folder{Path: displayPath(f), Collection: cl.Name, Files: total, State: "Aktuell", StateKind: "ok"}
			switch open := counts[store.StatusPending] + counts[store.StatusRunning]; {
			case open > 0:
				fo.State, fo.StateKind = fmt.Sprintf("%d Datei(en) in Arbeit", open), "running"
			case counts[store.StatusError]+counts[store.StatusOCR] > 0:
				fo.State, fo.StateKind = fmt.Sprintf("Aktuell · %d mit Hinweis", counts[store.StatusError]+counts[store.StatusOCR]), "info"
			case total == 0:
				fo.State, fo.StateKind = "Keine unterstützten Dateien gefunden", "info"
			}
			out = append(out, fo)
		}
	}
	return out, nil
}

// Documents lists indexed documents, optionally filtered by status.
func (s *AppService) Documents(status string) ([]Document, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	docs, err := c.st.Documents(s.appCtx, "", status)
	if err != nil {
		return nil, err
	}
	names := s.collectionNames(c)
	out := make([]Document, 0, len(docs))
	for _, d := range docs {
		mod := ""
		if d.ModTime > 0 {
			mod = time.Unix(d.ModTime, 0).Format("02.01.2006")
		}
		out = append(out, Document{
			Name: filepath.Base(d.Path), Dir: displayPath(filepath.Dir(d.Path)), FullPath: d.Path,
			Collection: names[d.CollectionID], Format: d.Format, Modified: mod,
			Status: d.Status, Note: d.Note, Chunks: d.Chunks,
		})
	}
	return out, nil
}

// Reindex puts one document at the front of the queue.
func (s *AppService) Reindex(path string) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	d, ok, err := c.st.DocumentByPath(s.appCtx, path)
	if err != nil || !ok {
		return errors.New("Dokument ist nicht im Index")
	}
	if c.ix == nil {
		return errors.New("Kein Embedding-Modell eingerichtet")
	}
	c.ix.Enqueue([]string{d.Path}, d.CollectionID, true)
	return nil
}

// OpenDocument opens an indexed file with its default app.
func (s *AppService) OpenDocument(path string) error {
	if err := s.checkIndexed(path); err != nil {
		return err
	}
	return application.Get().Browser.OpenFile(path)
}

// RevealDocument shows an indexed file in Finder / Explorer.
func (s *AppService) RevealDocument(path string) error {
	if err := s.checkIndexed(path); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}

// checkIndexed restricts file actions to files in the index.
func (s *AppService) checkIndexed(path string) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if _, ok, _ := c.st.DocumentByPath(s.appCtx, path); !ok {
		return errors.New("Dokument ist nicht im Index")
	}
	return nil
}

func (s *AppService) IndexProgress() IndexProgress {
	c, err := s.current()
	if err != nil || c.ix == nil {
		return IndexProgress{}
	}
	return toProgress(c.ix.Progress())
}

func (s *AppService) SetIndexPaused(paused bool) {
	if c, err := s.current(); err == nil && c.ix != nil {
		c.ix.SetPaused(paused)
	}
}

func toProgress(p indexer.Progress) IndexProgress {
	return IndexProgress{Active: p.Active, Paused: p.Paused, Collection: p.Collection, Done: p.Done, Total: p.Total, Current: p.Current}
}

// ---- Asking ----

// Search runs retrieval for a new turn and stores it. An empty
// conversationID starts a new conversation; otherwise the question is a
// follow-up and is rewritten into a standalone question for retrieval.
// mode is "answer" (then call Ask with the turn ID) or "search".
func (s *AppService) Search(conversationID, question, collectionID, mode string) (SearchResult, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return SearchResult{Sources: []Source{}}, errors.New("Leere Frage")
	}
	if mode != "search" {
		mode = "answer"
	}
	c, err := s.current()
	if err != nil {
		return SearchResult{}, err
	}
	start := time.Now()

	standalone := question
	if conversationID == "" {
		conversationID = askyourdocs.NewRandomHexID(8)
		if _, err := c.hist.Create(s.appCtx, conversationID, question, collectionID); err != nil {
			return SearchResult{}, err
		}
	} else {
		_, turns, err := c.hist.Get(s.appCtx, conversationID)
		if err != nil {
			return SearchResult{}, err
		}
		qa := make([]askyourdocs.QATurn, 0, len(turns))
		for _, t := range turns {
			qa = append(qa, askyourdocs.QATurn{Question: firstNonEmpty(t.Standalone, t.Question), Answer: citeMarks.ReplaceAllString(t.Answer, "")})
		}
		standalone = c.eng.Condense(s.appCtx, question, qa)
	}

	res, err := c.eng.Search(s.appCtx, standalone, collectionID)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{ConversationID: conversationID, Sources: make([]Source, 0, len(res.Results))}
	if standalone != question {
		out.Standalone = standalone
	}
	if res.VectorErr != nil {
		out.Notice = "Nur Volltextsuche: Das Embedding-Modell ist nicht erreichbar."
	}
	best := 0.0
	if len(res.Results) > 0 {
		best = res.Results[0].Hit.Score
	}
	for _, r := range res.Results {
		out.Sources = append(out.Sources, toSource(r, best))
	}
	snapshot, err := json.Marshal(out.Sources)
	if err != nil {
		return SearchResult{}, err
	}
	out.TurnID, err = c.hist.AddTurn(s.appCtx, history.Turn{
		ConversationID: conversationID, Question: question, Standalone: out.Standalone,
		Mode: mode, Sources: snapshot, Done: mode == "search",
	})
	if err != nil {
		return SearchResult{}, err
	}
	out.Millis = int(time.Since(start).Milliseconds())

	s.mu.Lock()
	s.searches[out.TurnID] = cachedSearch{question: standalone, results: res.Results}
	s.order = append(s.order, out.TurnID)
	if len(s.order) > 20 {
		delete(s.searches, s.order[0])
		s.order = s.order[1:]
	}
	s.mu.Unlock()
	return out, nil
}

// Ask streams the answer for a turn via answer:chunk events, finishes with
// answer:done and stores the result in the conversation.
func (s *AppService) Ask(turnID int64) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if !c.eng.HasModel() {
		return errors.New("Kein Antwortmodell eingerichtet – Suchmodus verwenden")
	}
	s.mu.RLock()
	cs, ok := s.searches[turnID]
	s.mu.RUnlock()
	if !ok {
		return errors.New("Suche abgelaufen, bitte erneut fragen")
	}
	go func() {
		var text strings.Builder
		lastSave := time.Now()
		ans, err := c.eng.Answer(s.appCtx, cs.question, cs.results, func(t string) {
			text.WriteString(t)
			s.emit(EventAnswerChunk, AnswerChunk{TurnID: turnID, Text: t})
			if time.Since(lastSave) > time.Second {
				_ = c.hist.SavePartial(s.appCtx, turnID, text.String())
				lastSave = time.Now()
			}
		})
		done := AnswerDone{TurnID: turnID, Confidence: ans.Confidence, Note: ans.Note}
		if err != nil {
			slog.Error("answer failed", "err", err)
			done.Error = "Die Antwort konnte nicht erzeugt werden: " + err.Error()
		}
		if err := c.hist.Finish(s.appCtx, turnID, text.String(), done.Confidence, done.Note, done.Error); err != nil {
			slog.Error("history: saving answer failed", "err", err)
		}
		s.emit(EventAnswerDone, done)
	}()
	return nil
}

// Conversations lists the most recent conversations.
func (s *AppService) Conversations() ([]ConversationSummary, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	list, err := c.hist.List(s.appCtx, 200)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationSummary, len(list))
	for i, cv := range list {
		out[i] = ConversationSummary{ID: cv.ID, Title: cv.Title, CollectionID: cv.CollectionID, Updated: relTime(cv.UpdatedAt)}
	}
	return out, nil
}

// Conversation returns a stored conversation with all turns.
func (s *AppService) Conversation(id string) (ConversationDetail, error) {
	c, err := s.current()
	if err != nil {
		return ConversationDetail{}, err
	}
	cv, turns, err := c.hist.Get(s.appCtx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	out := ConversationDetail{ID: cv.ID, Title: cv.Title, CollectionID: cv.CollectionID, Turns: make([]Turn, 0, len(turns))}
	for _, t := range turns {
		var src []Source
		if err := json.Unmarshal(t.Sources, &src); err != nil || src == nil {
			src = []Source{}
		}
		out.Turns = append(out.Turns, Turn{
			ID: t.ID, Question: t.Question, Standalone: t.Standalone, Mode: t.Mode, Sources: src,
			Answer: t.Answer, Confidence: t.Confidence, Note: t.Note, Error: t.Error, Done: t.Done,
		})
	}
	return out, nil
}

func (s *AppService) DeleteConversation(id string) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	return c.hist.Delete(s.appCtx, id)
}

func (s *AppService) DeleteAllConversations() error {
	c, err := s.current()
	if err != nil {
		return err
	}
	return c.hist.DeleteAll(s.appCtx)
}

var citeMarks = regexp.MustCompile(`\[\d+\]`)

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

// relTime labels a timestamp for the history list.
func relTime(t time.Time) string {
	now := time.Now()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case now.Sub(t) < 48*time.Hour && now.AddDate(0, 0, -1).Day() == d2:
		return "Gestern"
	case y1 == y2:
		return t.Format("02.01.")
	}
	return t.Format("02.01.2006")
}

// ---- Diagnostics ----

// LogLevel returns the current log level (debug, info, warn, error).
func (s *AppService) LogLevel() string {
	if s.logs == nil {
		return "info"
	}
	return s.logs.Level()
}

// SetLogLevel changes the level now and keeps it for the next start.
func (s *AppService) SetLogLevel(level string) error {
	if _, err := logging.ParseLevel(level); err != nil {
		return err
	}
	if s.logs != nil {
		if err := s.logs.SetLevel(level); err != nil {
			return err
		}
	}
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	cfg.LogLevel = level
	return s.saveSettings(cfg)
}

// RevealLogs opens the log folder in Finder / Explorer.
func (s *AppService) RevealLogs() error {
	if s.logs == nil {
		return errors.New("Kein Protokoll eingerichtet")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", s.logs.Dir).Start()
	case "windows":
		return exec.Command("explorer", s.logs.Dir).Start()
	default:
		return exec.Command("xdg-open", s.logs.Dir).Start()
	}
}

// Diagnostics returns a plain-text report for bug reports: versions,
// settings, index statistics and the last log lines. It names no documents
// or questions beyond what the log holds at its level; the UI shows it
// before anything is copied.
func (s *AppService) Diagnostics() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	s.mu.RLock()
	cfg, st := s.settings, s.status
	s.mu.RUnlock()

	line("PersonalRAG %s", version)
	line("System: %s/%s, %s, Wails %s", runtime.GOOS, runtime.GOARCH, runtime.Version(), wailsVersion())
	line("Datenverzeichnis: %s", displayPath(s.dataDir))
	line("Protokollstufe: %s", s.LogLevel())
	emb := "keins"
	if cfg.Embedding != "" {
		emb = fmt.Sprintf("%s (%d Dimensionen)", cfg.Embedding, cfg.EmbeddingDim)
	}
	llm := "keins (nur Suche)"
	if cfg.LLM != "" {
		llm = cfg.LLM
	}
	line("Modelle: Embedding %s, Antwort %s", emb, llm)
	ollama := "erreichbar"
	ctx, cancel := context.WithTimeout(s.appCtx, 3*time.Second)
	if _, err := rag.OllamaModels(ctx, cfg.OllamaHost); err != nil {
		ollama = "nicht erreichbar"
	}
	cancel()
	line("Ollama: %s, %s", cfg.OllamaHost, ollama)
	line("Status: bereit=%v, Einrichtung abgeschlossen=%v", st.Ready, st.SetupDone)
	if st.Error != "" {
		line("Fehler: %s", st.Error)
	}

	if c, err := s.current(); err == nil {
		colls, _ := c.st.Collections(s.appCtx)
		counts, _ := c.st.StatusCounts(s.appCtx)
		total := 0
		var parts []string
		for _, k := range []string{store.StatusOK, store.StatusPending, store.StatusRunning, store.StatusError, store.StatusOCR, store.StatusCloud} {
			if n := counts[k]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", k, n))
				total += n
			}
		}
		line("Index: %d Sammlungen, %d Dokumente (%s)", len(colls), total, strings.Join(parts, ", "))
		convs, _ := c.hist.List(s.appCtx, 1_000_000)
		line("Verlauf: %d Unterhaltungen", len(convs))
		vi, _ := migrate.Version(s.appCtx, c.st.DB(), "index")
		vh, _ := migrate.Version(s.appCtx, c.st.DB(), "history")
		line("Schema: index v%d, history v%d", vi, vh)
		if c.ix != nil {
			p := c.ix.Progress()
			line("Indexierung: aktiv=%v, pausiert=%v, %d/%d", p.Active, p.Paused, p.Done, p.Total)
		}
	}

	if s.crashText != "" {
		line("")
		line("--- Absturz am %s ---", s.lastCrash)
		line("%s", redactHome(s.crashText))
	}
	if s.logs != nil {
		line("")
		line("--- Protokoll (letzte 200 Zeilen) ---")
		for _, l := range s.logs.Tail(200) {
			line("%s", redactHome(l))
		}
	}
	return b.String()
}

// redactHome replaces the home directory with ~ in text.
func redactHome(text string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return text
	}
	return strings.ReplaceAll(text, home, "~")
}

func wailsVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/wailsapp/wails/v3" {
				return d.Version
			}
		}
	}
	return "unbekannt"
}

// ---- Agents (MCP): UI only, server follows ----

func (s *AppService) MCPStatus() MCPStatus {
	return MCPStatus{Note: "Der MCP-Server ist noch nicht eingebaut. Freigaben werden schon gespeichert."}
}

func (s *AppService) ClientConfigs() []ClientConfig {
	exe, err := os.Executable()
	if err != nil {
		exe = "PersonalRAG"
	}
	return []ClientConfig{
		{
			Name: "Claude Desktop",
			Hint: "In claude_desktop_config.json eintragen. Claude Desktop startet die App als stdio-Brücke.",
			Snippet: fmt.Sprintf(`{
  "mcpServers": {
    "personalrag": {
      "command": %q,
      "args": ["--mcp"]
    }
  }
}`, exe),
		},
		{
			Name:    "Claude Code",
			Hint:    "Im Terminal ausführen (stdio-Brücke).",
			Snippet: fmt.Sprintf(`claude mcp add personalrag -- %q --mcp`, exe),
		},
		{
			Name: "Cursor",
			Hint: "In ~/.cursor/mcp.json eintragen.",
			Snippet: fmt.Sprintf(`{
  "mcpServers": {
    "personalrag": { "command": %q, "args": ["--mcp"] }
  }
}`, exe),
		},
	}
}

func (s *AppService) AccessLog() []AccessLogEntry { return []AccessLogEntry{} }

// ---- helpers ----

func (s *AppService) collectionNames(c *core) map[string]string {
	colls, _ := c.st.Collections(s.appCtx)
	m := make(map[string]string, len(colls))
	for _, cl := range colls {
		m[cl.ID] = cl.Name
	}
	return m
}

func (s *AppService) collectionName(id string) string {
	c, err := s.current()
	if err != nil {
		return id
	}
	if n, ok := s.collectionNames(c)[id]; ok {
		return n
	}
	return id
}

func toSource(r rag.Result, best float64) Source {
	h := r.Hit
	var loc []string
	if p := rag.Pages(h.Pages); p != "" {
		if strings.Contains(p, "-") {
			loc = append(loc, "Seiten "+p)
		} else {
			loc = append(loc, "Seite "+p)
		}
	}
	section := h.Section
	if section == "" {
		section = h.Breadcrumb
	}
	if section != "" {
		loc = append(loc, section)
	}
	if len(loc) == 0 {
		loc = append(loc, strings.ToUpper(strings.TrimPrefix(filepath.Ext(h.Path), ".")))
	}
	score := 0.0
	if best > 0 {
		score = h.Score / best
	}
	text := headingPrefix.ReplaceAllString(h.Text, "")
	before, hl, after := splitParent(h.ParentText, text)
	return Source{
		N: r.N, ChunkID: h.ChunkID, File: filepath.Base(h.Path), Dir: displayPath(filepath.Dir(h.Path)), FullPath: h.Path,
		Location: strings.Join(loc, " · "), Section: section, Score: score,
		Short: truncate(oneLine(text), 150), Before: before, Highlight: hl, After: after,
	}
}

// splitParent locates the chunk inside its parent window and returns a short
// stretch of context on each side, cut at sentence or word boundaries.
func splitParent(parent, chunk string) (string, string, string) {
	parent = headingPrefix.ReplaceAllString(parent, "")
	i := strings.Index(parent, chunk)
	if parent == "" || i < 0 {
		return "", chunk, ""
	}
	return contextBefore(parent[:i], 280), chunk, contextAfter(parent[i+len(chunk):], 280)
}

// contextBefore keeps the last ~n runes of s, starting at a sentence start
// if one is near, else at a word start.
func contextBefore(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	tail := string(r[len(r)-n:])
	if j := sentenceStart.FindStringIndex(tail); j != nil && j[1] < len(tail)/2 {
		return "… " + strings.TrimSpace(tail[j[1]:])
	}
	if j := strings.IndexAny(tail, " \n"); j >= 0 {
		tail = tail[j+1:]
	}
	return "… " + strings.TrimSpace(tail)
}

// contextAfter keeps the first ~n runes of s, ending at a sentence end if one
// is near, else at a word end.
func contextAfter(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := string(r[:n])
	if j := strings.LastIndexAny(head, ".!?"); j > len(head)/2 {
		return head[:j+1] + " …"
	}
	if j := strings.LastIndexAny(head, " \n"); j > 0 {
		head = head[:j]
	}
	return strings.TrimSpace(head) + " …"
}

var sentenceStart = regexp.MustCompile(`[.!?:]\s+`)

var spaces = regexp.MustCompile(`\s+`)

// headingPrefix is the "[Breadcrumb]\n\n" BuildChunks puts in front of a
// chunk for embedding; it is not part of the document text.
var headingPrefix = regexp.MustCompile(`^\[[^\]\n]*\]\s*\n\s*`)

func oneLine(s string) string { return strings.TrimSpace(spaces.ReplaceAllString(s, " ")) }

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + " …"
	}
	return s
}

func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && paths.Within(p, home) {
		return "~" + p[len(filepath.Clean(home)):]
	}
	return p
}

func modelName(spec string) string {
	if spec == "" {
		return ""
	}
	_, name := askyourdocs.ParseModelSpec(spec)
	return name
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(name string) string {
	r := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")
	s := strings.Trim(nonSlug.ReplaceAllString(r.Replace(strings.ToLower(name)), "-"), "-")
	if s == "" {
		s = "sammlung"
	}
	return s
}

func uniqueID(base string, colls []store.Collection) string {
	taken := map[string]bool{}
	for _, c := range colls {
		taken[c.ID] = true
	}
	id := base
	for i := 2; taken[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}
