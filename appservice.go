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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/agent"
	"github.com/dbis-ilm/personalrag/internal/config"
	"github.com/dbis-ilm/personalrag/internal/history"
	"github.com/dbis-ilm/personalrag/internal/i18n"
	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/ingest"
	"github.com/dbis-ilm/personalrag/internal/logging"
	"github.com/dbis-ilm/personalrag/internal/migrate"
	"github.com/dbis-ilm/personalrag/internal/paths"
	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/secrets"
	"github.com/dbis-ilm/personalrag/internal/store"
)

// Event names emitted to the frontend.
const (
	EventAnswerChunk    = "answer:chunk"
	EventAnswerDone     = "answer:done"
	EventIndexProgress  = "index:progress"
	EventSourcesChanged = "sources:changed"
	EventStatusChanged  = "status:changed"
	EventNotice         = "notice"
	EventShowAbout      = "about:show"
	EventAnswerSources  = "answer:sources"
)

// ShowAbout asks the window to open the About box (from the app or tray menu).
type ShowAbout struct{}

// repository is the project's public home, linked from the About box.
const repository = "https://github.com/dbis-ilm/personalrag"

const rescanInterval = 15 * time.Minute

var collectionColors = []string{"#2452B8", "#B4651C", "#1F8A4C", "#7A4BC4", "#B23A6B", "#2C7A86", "#6B7079"}

// ---- Types shared with the frontend (TS types are generated) ----

type AppStatus struct {
	SetupDone  bool   `json:"setupDone"`
	Ready      bool   `json:"ready"`
	SearchOnly bool   `json:"searchOnly"` // no answer model
	Agent      string `json:"agent"`      // model of the agent mode; "" = not available
	// AgentRemote is the host the agent model runs on when that is another
	// machine: tasks, document lists and excerpts go there.
	AgentRemote string `json:"agentRemote"`
	// AgentTextLocal: the agent gets no document text, only extract results.
	AgentTextLocal bool   `json:"agentTextLocal"`
	LLM            string `json:"llm"`
	Embedding      string `json:"embedding"`
	Error          string `json:"error"`
	DataDir        string `json:"dataDir"`
	LastCrash      string `json:"lastCrash"` // when the previous run crashed (RFC 3339); "" if it did not
	// Language is the language in use ("de" | "en"); LanguageSetting what
	// the user chose ("" = follow the system).
	Language        string `json:"language"`
	LanguageSetting string `json:"languageSetting"`
	// Remote is the host that receives document excerpts ("" = all local);
	// LLMProvider is "Ollama" or "GPUStack".
	Remote      string `json:"remote"`
	LLMProvider string `json:"llmProvider"`
}

type Collection struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Color            string   `json:"color"`
	DocCount         int      `json:"docCount"`
	SharedWithAgents bool     `json:"sharedWithAgents"`
	Folders          []string `json:"folders"`
	Instructions     string   `json:"instructions"`
}

// ConversationSummary is one entry of the persistent chat history.
type ConversationSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CollectionID string `json:"collectionId"`
	UpdatedAt    int64  `json:"updatedAt"` // unix milliseconds
}

// Turn is one question with its sources and answer.
type Turn struct {
	ID         int64    `json:"id"`
	Question   string   `json:"question"`
	Standalone string   `json:"standalone"` // follow-up rewritten for search; "" if unchanged
	Mode       string   `json:"mode"`       // "answer" | "search"
	Sources    []Source `json:"sources"`
	HasMore    bool     `json:"hasMore"` // further matches may exist (search: show more, answer: AskMore)
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
	HasMore        bool     `json:"hasMore"` // MoreSources may find further matches
	Millis         int      `json:"millis"`
	Notice         string   `json:"notice"`
}

// MoreResult is the next page of a search turn's sources.
type MoreResult struct {
	Sources []Source `json:"sources"`
	HasMore bool     `json:"hasMore"`
}

const (
	morePage = 8  // sources per "show more"
	maxShown = 96 // sources a turn shows at most
)

type AnswerChunk struct {
	TurnID int64  `json:"turnId"`
	Text   string `json:"text"`
}

type AnswerDone struct {
	TurnID     int64  `json:"turnId"`
	Confidence string `json:"confidence"` // "high" | "medium" | "low"
	Note       string `json:"note"`
	Error      string `json:"error"`
	HasMore    bool   `json:"hasMore"` // AskMore may find further matches
}

// AnswerSources carries sources added to a turn by AskMore, sent before
// the text that cites them.
type AnswerSources struct {
	TurnID  int64    `json:"turnId"`
	Sources []Source `json:"sources"`
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
	State      string `json:"state"`     // "current" | "working" | "notes" | "empty"
	Count      int    `json:"count"`     // files in work or with notes
	StateKind  string `json:"stateKind"` // "ok" | "running" | "info"
}

type Document struct {
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	FullPath   string `json:"fullPath"`
	Collection string `json:"collection"`
	Format     string `json:"format"`
	ModTime    int64  `json:"modTime"` // unix milliseconds, 0 if unknown
	Status     string `json:"status"`  // pending | running | ok | error | ocr | cloud
	Note       string `json:"note"`    // "@code" for known notes (see indexer), else plain text
	Chunks     int    `json:"chunks"`
}

type Notice struct {
	Text string `json:"text"`
	Kind string `json:"kind"` // "ok" | "warn" | "error"
}

type SourcesChanged struct {
	At int64 `json:"at"`
}

type MCPStatus struct {
	Running     bool   `json:"running"`
	Address     string `json:"address"`
	TokenHint   string `json:"tokenHint"`
	SharedCount int    `json:"sharedCount"` // collections visible to agents
}

type ClientConfig struct {
	Name    string `json:"name"`
	Hint    string `json:"hint"`
	Snippet string `json:"snippet"`
}

type AccessLogEntry struct {
	At         int64  `json:"at"` // unix milliseconds
	Client     string `json:"client"`
	Tool       string `json:"tool"`
	Query      string `json:"query"`
	Result     string `json:"result"`
	Kind       string `json:"kind"`
	DurationMs int64  `json:"durationMs"`
}

// ---- Service ----

// AppService is the app's API for the UI. It owns the index, the indexer
// and the query engine ("core"), which is rebuilt when models change.
type AppService struct {
	dataDir string
	appCtx  context.Context
	logs    *logging.Logs // nil in tests

	// agentMode: serving MCP next to the app (PersonalRAG --mcp). Reads the
	// index and logs agent calls, but never indexes or resets anything.
	agentMode bool

	lastCrash string // label of the previous run's crash, if any
	crashText string

	mu        sync.RWMutex
	settings  config.Settings
	core      *core
	status    AppStatus
	searches  map[int64]cachedSearch
	extending map[int64]bool               // turns AskMore is working on
	agentRuns map[int64]context.CancelFunc // running agent tasks by turn
	order     []int64

	nextID atomic.Int64

	// restartMu serialises core restarts, so two overlapping restarts can
	// never both open the index; restarts tracks the asynchronous ones and
	// closing stops new ones once the service shuts down.
	onLanguage func()

	restartMu sync.Mutex
	restarts  sync.WaitGroup
	closing   bool
	debounce  *time.Timer
	debMu     sync.Mutex
}

type cachedSearch struct {
	question     string
	results      []rag.Result
	instructions string // the collection's notes for the answer model
}

// agentRuntime is what the agent mode needs: its model and the tools,
// registered on the core's Genkit.
type agentRuntime struct {
	spec      string
	textLocal bool // no document text in tool results
	g         *genkit.Genkit
	model     ai.Model
	config    any
	tools     []ai.ToolRef
}

type core struct {
	agent  *agentRuntime // nil without a model for the agent mode
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
			s.lastCrash, s.crashText = when.Format(time.RFC3339), text
			slog.Warn("previous run crashed", "at", when)
		}
	}
	st, err := config.Load(s.dataDir)
	if s.agentMode {
		i18n.Set(i18n.EN) // texts for agents are always English
	} else {
		i18n.Set(i18n.Resolve(st.Language))
	}
	if err != nil {
		slog.Warn("settings unreadable, using defaults", "err", err)
	}
	s.settings = st
	s.restartCore()
	return nil
}

func (s *AppService) ServiceShutdown() error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.restarts.Wait() // a restart in flight must not reopen the index after this
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
// restartAsync rebuilds the core in the background unless the service is
// shutting down.
func (s *AppService) restartAsync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return
	}
	s.restarts.Add(1)
	go func() {
		defer s.restarts.Done()
		s.restartCore()
	}()
}

func (s *AppService) restartCore() {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
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
		LastCrash: s.lastCrash, Remote: remoteHost(cfg), LLMProvider: providerLabel(providerOf(cfg.LLM)),
		Language: string(i18n.Current()), LanguageSetting: cfg.Language,
	}
	if err != nil {
		slog.Error("core start failed", "err", err)
		status.Error = err.Error()
	} else {
		status.Ready = true
		if c.agent != nil {
			status.Agent = modelName(c.agent.spec)
			status.AgentTextLocal = c.agent.textLocal
			if providerOf(c.agent.spec) == providerGPUStack && !askyourdocs.IsLocalURL(cfg.GPUStackURL) {
				status.AgentRemote = askyourdocs.URLHost(cfg.GPUStackURL)
			}
		}
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		if c != nil {
			c.stop()
		}
		return
	}
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

	if err := checkConsent(cfg); err != nil {
		return fail(err)
	}
	gpuKey := ""
	if cfg.GPUStackURL != "" {
		var err error
		if gpuKey, err = secrets.Get(secrets.GPUStackKey); err != nil {
			return fail(i18n.Errorf("core.keychain", err))
		}
	}

	var specs []string
	if cfg.Embedding != "" {
		specs = append(specs, cfg.Embedding)
	}
	if cfg.LLM != "" {
		specs = append(specs, cfg.LLM)
	}
	if cfg.AgentModel != "" {
		specs = append(specs, cfg.AgentModel)
	}
	providers, err := askyourdocs.NewProviders(
		askyourdocs.ProviderConfig{OllamaHost: cfg.OllamaHost, GPUStackURL: cfg.GPUStackURL, GPUStackAPIKey: gpuKey, Timeout: 3 * time.Minute},
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
		if s.agentMode {
			// Re-indexing is the app's job; agents wait for it.
			return fail(i18n.Errorf("core.rebuilding"))
		}
		err = c.st.ResetVectors()
	}
	if err != nil {
		if errors.Is(err, migrate.ErrTooNew) {
			return fail(i18n.Errorf("core.tooNew"))
		}
		return fail(i18n.Errorf("core.openIndex", err))
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
	if spec := firstNonEmpty(cfg.AgentModel, cfg.LLM); spec != "" && !s.agentMode {
		// The agent plans with its own model; extract inside its tools
		// uses the answer model, like for MCP clients. Without a usable
		// model the rest of the app still works.
		if m, err := providers.Model(g, spec); err != nil {
			slog.Warn("agent model unavailable", "model", spec, "err", err)
		} else {
			srv := agent.New(c.st, c.eng, version).InApp()
			if cfg.AgentTextLocal {
				srv = srv.TextLocal()
			}
			c.agent = &agentRuntime{
				spec: spec, textLocal: cfg.AgentTextLocal, g: g, model: m,
				config: askyourdocs.GenerateConfig(spec, askyourdocs.GenerateOptions{NumCtx: 32768, KeepAlive: "30m"}),
				tools:  srv.Tools(g),
			}
		}
	}
	if c.hist, err = history.New(c.st.DB(), filepath.Join(s.dataDir, "index.db.history.bak")); err != nil {
		return fail(err)
	}
	if s.agentMode {
		// The app may be streaming an answer right now; leave its turns alone.
	} else if err := c.hist.MarkInterrupted(ctx); err != nil {
		slog.Warn("history: marking interrupted answers failed", "err", err)
	}

	if emb != nil && !s.agentMode {
		c.ix = indexer.New(g, c.st, func(p indexer.Progress) { s.emit(EventIndexProgress, toProgress(p)) }, s.sourcesChanged)
		c.ix.SetExcludes(cfg.Exclude)
		c.wg.Add(3)
		go func() { defer c.wg.Done(); c.ix.Run(ctx) }()
		go func() {
			defer c.wg.Done()
			if err := c.ix.Watch(ctx); err != nil {
				slog.Warn("file watcher unavailable; changes are picked up by the periodic scan", "err", err)
			}
		}()
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
		return nil, i18n.Errorf("core.notReady")
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
		out[i] = Collection{ID: cl.ID, Name: cl.Name, Color: cl.Color, DocCount: cl.DocCount, SharedWithAgents: cl.SharedWithAgents, Folders: cl.Folders, Instructions: cl.Instructions}
	}
	return out, nil
}

// AddFolder asks for a folder and turns it into a new collection. An empty
// ID in the result means the dialog was cancelled.
func (s *AppService) AddFolder() (Collection, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).CanChooseFiles(false).
		SetTitle(i18n.T("dialog.addFolder")).SetButtonText(i18n.T("dialog.add")).
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
		return Collection{}, i18n.Errorf("folder.notADir", path)
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
			return i18n.Errorf("folder.inCollection", s.collectionName(f.CollectionID))
		case paths.Within(f.Path, path):
			return i18n.Errorf("folder.contains", displayPath(f.Path), s.collectionName(f.CollectionID))
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
		parts = append(parts, i18n.T("drop.foldersAdded", added))
	}
	if queued > 0 {
		parts = append(parts, i18n.T("drop.filesQueued", queued))
	}
	if outside > 0 {
		parts = append(parts, i18n.T("drop.filesOutside", outside))
	}
	if unsupported > 0 {
		parts = append(parts, i18n.T("drop.filesUnsupported", unsupported))
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
	if c.ix != nil {
		c.ix.RefreshWatch(s.appCtx)
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

// SetCollectionInstructions stores the user's notes for answers from a
// collection (style, terms, background). They are added to the answer
// prompts and the agent's task, below the prompts' own rules.
func (s *AppService) SetCollectionInstructions(id, text string) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if err := c.st.SetCollectionInstructions(s.appCtx, id, rag.Instructions(text)); err != nil {
		return err
	}
	s.emit(EventSourcesChanged, SourcesChanged{At: time.Now().UnixMilli()})
	return nil
}

// Rescan checks all folders for changes now.
func (s *AppService) Rescan() error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if c.ix == nil {
		return i18n.Errorf("core.noEmbedding")
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
			fo := Folder{Path: displayPath(f), Collection: cl.Name, Files: total, State: "current", StateKind: "ok"}
			switch open := counts[store.StatusPending] + counts[store.StatusRunning]; {
			case open > 0:
				fo.State, fo.Count, fo.StateKind = "working", open, "running"
			case counts[store.StatusError]+counts[store.StatusOCR] > 0:
				fo.State, fo.Count, fo.StateKind = "notes", counts[store.StatusError]+counts[store.StatusOCR], "info"
			case total == 0:
				fo.State, fo.StateKind = "empty", "info"
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
		out = append(out, Document{
			Name: filepath.Base(d.Path), Dir: displayPath(filepath.Dir(d.Path)), FullPath: d.Path,
			Collection: names[d.CollectionID], Format: d.Format, ModTime: d.ModTime * 1000,
			Status: d.Status, Note: noteCode(d.Note), Chunks: d.Chunks,
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
		return i18n.Errorf("doc.notIndexed")
	}
	if c.ix == nil {
		return i18n.Errorf("core.noEmbedding")
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
		return i18n.Errorf("doc.notIndexed")
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
		return SearchResult{Sources: []Source{}}, i18n.Errorf("search.empty")
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
		out.Notice = i18n.T("search.keywordOnly")
	}
	best := 0.0
	if len(res.Results) > 0 {
		best = res.Results[0].Hit.Score
	}
	for _, r := range res.Results {
		out.Sources = append(out.Sources, toSource(r, best))
	}
	out.HasMore = len(res.Results) == rag.MaxSources
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
	s.searches[out.TurnID] = cachedSearch{question: standalone, results: res.Results, instructions: c.st.CollectionInstructions(s.appCtx, collectionID)}
	s.order = append(s.order, out.TurnID)
	if len(s.order) > 20 {
		delete(s.searches, s.order[0])
		s.order = s.order[1:]
	}
	s.mu.Unlock()
	return out, nil
}

// mayHaveMore guesses for a stored turn whether "show more" could
// find further matches: every page so far came back full. A wrong guess
// costs one click that finds nothing.
func mayHaveMore(shown int) bool {
	return shown >= rag.MaxSources && (shown-rag.MaxSources)%morePage == 0 && shown < maxShown
}

// MoreSources finds the next matches of a search turn and appends them to
// its stored sources.
func (s *AppService) MoreSources(turnID int64) (MoreResult, error) {
	c, err := s.current()
	if err != nil {
		return MoreResult{}, err
	}
	p, err := s.nextPage(c, turnID)
	if err != nil {
		return MoreResult{}, err
	}
	return MoreResult{Sources: p.sources, HasMore: p.hasMore}, nil
}

// page is one "show more" step: the new sources for the UI, the same
// matches for the answer model, and whether more may follow.
type page struct {
	question     string
	sources      []Source
	results      []rag.Result
	shown        int // sources of the turn including this page
	hasMore      bool
	instructions string // the collection's notes for the answer model
}

// nextPage finds the next matches of a turn and appends them to its stored
// sources. Matches already shown are skipped, since a deeper search can
// reorder the fused ranking a little.
func (s *AppService) nextPage(c *core, turnID int64) (page, error) {
	t, err := c.hist.Turn(s.appCtx, turnID)
	if err != nil {
		return page{}, err
	}
	conv, _, err := c.hist.Get(s.appCtx, t.ConversationID)
	if err != nil {
		return page{}, err
	}
	var shown []Source
	if len(t.Sources) > 0 {
		if err := json.Unmarshal(t.Sources, &shown); err != nil {
			return page{}, err
		}
	}
	out := page{question: firstNonEmpty(t.Standalone, t.Question), sources: []Source{}, shown: len(shown), instructions: c.st.CollectionInstructions(s.appCtx, conv.CollectionID)}
	if len(shown) >= maxShown {
		return out, nil
	}
	want := min(len(shown)+morePage, maxShown)
	res, err := c.eng.SearchN(s.appCtx, out.question, conv.CollectionID, want)
	if err != nil {
		return page{}, err
	}
	seen := make(map[int64]bool, len(shown))
	for _, src := range shown {
		seen[src.ChunkID] = true
	}
	best := 0.0
	if len(res.Results) > 0 {
		best = res.Results[0].Hit.Score
	}
	for _, r := range res.Results {
		if seen[r.Hit.ChunkID] || len(out.sources) == morePage {
			continue
		}
		r.N = len(shown) + len(out.sources) + 1
		out.sources = append(out.sources, toSource(r, best))
		out.results = append(out.results, r)
	}
	all := append(shown, out.sources...)
	out.shown = len(all)
	out.hasMore = len(res.Results) == want && len(out.sources) == morePage && len(all) < maxShown
	snapshot, err := json.Marshal(all)
	if err != nil {
		return page{}, err
	}
	if err := c.hist.SetSources(s.appCtx, turnID, snapshot); err != nil {
		return page{}, err
	}
	return out, nil
}

// AskMore extends an answer with the matches beyond the first page, for
// questions like "show me all …": page by page (8 matches) it rates the
// next matches and appends what the relevant ones add, until a page has
// no relevant match, the matches run out or 96 are shown. New sources
// arrive as answer:sources events before the text citing them; the end is
// an answer:done event as for Ask.
func (s *AppService) AskMore(turnID int64) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if !c.eng.HasModel() {
		return i18n.Errorf("core.noAnswerModel")
	}
	t, err := c.hist.Turn(s.appCtx, turnID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.extending[turnID] {
		s.mu.Unlock()
		return nil // already running
	}
	if s.extending == nil {
		s.extending = map[int64]bool{}
	}
	s.extending[turnID] = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.extending, turnID)
			s.mu.Unlock()
		}()
		answer := t.Answer
		done := AnswerDone{TurnID: turnID, Confidence: confidenceCode(t.Confidence)}
		var lastErr error
		shown, added := 0, 0
		for {
			p, err := s.nextPage(c, turnID)
			if err != nil {
				lastErr = err
				break
			}
			shown = p.shown
			done.HasMore = p.hasMore
			if len(p.sources) == 0 {
				done.HasMore = false
				break
			}
			s.emit(EventAnswerSources, AnswerSources{TurnID: turnID, Sources: p.sources})
			more, err := c.eng.AnswerMore(s.appCtx, p.question, p.instructions, answer, p.results)
			if err != nil {
				lastErr = err
				break
			}
			if more.Text != "" {
				chunk := "\n\n" + more.Text
				answer += chunk
				added++
				s.emit(EventAnswerChunk, AnswerChunk{TurnID: turnID, Text: chunk})
				_ = c.hist.SavePartial(s.appCtx, turnID, answer)
			}
			if more.Relevant == 0 || !p.hasMore {
				break // a page without a relevant match ends the search
			}
		}
		done.Note = i18n.T("answer.moreDone", len(citedNumbers(answer)), shown)
		if added == 0 && lastErr == nil {
			done.Note = i18n.T("answer.moreNothing", shown)
		}
		if lastErr != nil {
			slog.Error("extending answer failed", "err", lastErr)
			done.Error = i18n.T("answer.failed", lastErr.Error())
		}
		if err := c.hist.Finish(s.appCtx, turnID, answer, done.Confidence, done.Note, done.Error); err != nil {
			slog.Error("history: saving answer failed", "err", err)
		}
		s.emit(EventAnswerDone, done)
	}()
	return nil
}

// citedNumbers returns the distinct source numbers an answer cites.
func citedNumbers(answer string) map[int]bool {
	out := map[int]bool{}
	for _, m := range citeNumber.FindAllStringSubmatch(answer, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out[n] = true
		}
	}
	return out
}

var citeNumber = regexp.MustCompile(`\[(\d+)\]`)

// Ask streams the answer for a turn via answer:chunk events, finishes with
// answer:done and stores the result in the conversation.
func (s *AppService) Ask(turnID int64) error {
	c, err := s.current()
	if err != nil {
		return err
	}
	if !c.eng.HasModel() {
		return i18n.Errorf("core.noAnswerModel")
	}
	s.mu.RLock()
	cs, ok := s.searches[turnID]
	s.mu.RUnlock()
	if !ok {
		return i18n.Errorf("search.expired")
	}
	go func() {
		var text strings.Builder
		lastSave := time.Now()
		ans, err := c.eng.Answer(s.appCtx, cs.question, cs.instructions, cs.results, func(t string) {
			text.WriteString(t)
			s.emit(EventAnswerChunk, AnswerChunk{TurnID: turnID, Text: t})
			if time.Since(lastSave) > time.Second {
				_ = c.hist.SavePartial(s.appCtx, turnID, text.String())
				lastSave = time.Now()
			}
		})
		done := AnswerDone{TurnID: turnID, Confidence: string(ans.Confidence), Note: answerNote(ans), HasMore: err == nil && len(cs.results) == rag.MaxSources}
		if err != nil {
			slog.Error("answer failed", "err", err)
			done.Error = i18n.T("answer.failed", err.Error())
		}
		if err := c.hist.Finish(s.appCtx, turnID, text.String(), done.Confidence, done.Note, done.Error); err != nil {
			slog.Error("history: saving answer failed", "err", err)
		}
		s.emit(EventAnswerDone, done)
	}()
	return nil
}

// RecentQuestions lists the questions asked before, newest first and each
// once, for paging through them in the question field.
func (s *AppService) RecentQuestions() ([]string, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	return c.hist.RecentQuestions(s.appCtx, 200)
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
		out[i] = ConversationSummary{ID: cv.ID, Title: cv.Title, CollectionID: cv.CollectionID, UpdatedAt: cv.UpdatedAt.UnixMilli()}
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
			HasMore: mayHaveMore(len(src)),
			Answer:  t.Answer, Confidence: confidenceCode(t.Confidence), Note: t.Note, Error: t.Error, Done: t.Done,
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

// legacyNotes maps the German document notes stored by earlier versions to
// the codes the UI translates.
var legacyNotes = map[string]string{
	"Datei nicht lesbar":                                  indexer.NoteUnreadable,
	"Keine Textebene erkannt":                             indexer.NoteNeedsOCR,
	"Embedding-Modell nicht erreichbar":                   indexer.NoteEmbeddingUnreachable,
	"Kein Text gefunden":                                  indexer.NoteNoText,
	"Datei ist passwortgeschützt":                         indexer.NoteEncrypted,
	"Datei ist beschädigt oder hat ein ungültiges Format": indexer.NoteCorrupt,
}

func noteCode(n string) string {
	if c, ok := legacyNotes[n]; ok {
		return c
	}
	return n
}

// confidenceCode maps the German labels stored by earlier versions to codes.
func confidenceCode(c string) string {
	switch c {
	case "hoch":
		return "high"
	case "mittel":
		return "medium"
	case "niedrig":
		return "low"
	}
	return c
}

// answerNote words how the sources were rated, in the current language.
func answerNote(a rag.Answer) string {
	switch {
	case a.Total == 0:
		return i18n.T("answer.noHits")
	case a.Fallback:
		return i18n.T("answer.fallback")
	}
	if left := a.Relevant - len(a.Used); left > 0 {
		return i18n.T("answer.usedBudget", len(a.Used), a.Total, left)
	}
	return i18n.T("answer.used", len(a.Used), a.Total)
}

// ---- Language ----

// SetLanguage sets the UI language: "de", "en" or "" to follow the system.
// Go-side texts switch at once; the tray menu follows via onLanguage.
func (s *AppService) SetLanguage(setting string) error {
	switch setting {
	case "", "de", "en":
	default:
		return fmt.Errorf("unsupported language %q", setting)
	}
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	cfg.Language = setting
	if err := s.saveSettings(cfg); err != nil {
		return err
	}
	lang := i18n.Resolve(setting)
	i18n.Set(lang)
	s.mu.Lock()
	s.status.Language, s.status.LanguageSetting = string(lang), setting
	st := s.status
	hook := s.onLanguage
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	s.emit(EventStatusChanged, st)
	return nil
}

// onLanguageChange registers a callback run after the language changed,
// e.g. to relabel the tray menu.
func (s *AppService) onLanguageChange(f func()) {
	s.mu.Lock()
	s.onLanguage = f
	s.mu.Unlock()
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
		return i18n.Errorf("core.noLog")
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

	line("PersonalRAG %s (%s), askyourdocs %s", version, firstNonEmpty(buildCommit(), "dev"), depVersion("github.com/dbis-ilm/askyourdocs"))
	line("System: %s/%s, %s, Wails %s", runtime.GOOS, runtime.GOARCH, runtime.Version(), wailsVersion())
	line("Data directory: %s", displayPath(s.dataDir))
	line("Log level: %s, language: %s", s.LogLevel(), i18n.Current())
	emb := "none"
	if cfg.Embedding != "" {
		emb = fmt.Sprintf("%s (%d Dimensionen)", cfg.Embedding, cfg.EmbeddingDim)
	}
	llm := "none (search only)"
	if cfg.LLM != "" {
		llm = cfg.LLM
	}
	line("Models: embedding %s, answers %s", emb, llm)
	ollama := "reachable"
	ctx, cancel := context.WithTimeout(s.appCtx, 3*time.Second)
	if cfg.OllamaOff {
		ollama = "off"
	} else if _, err := askyourdocs.ListOllamaModels(ctx, cfg.OllamaHost); err != nil {
		ollama = "not reachable"
	}
	cancel()
	line("Ollama: %s, %s", cfg.OllamaHost, ollama)
	line("Status: ready=%v, setup done=%v", st.Ready, st.SetupDone)
	if st.Error != "" {
		line("Error: %s", st.Error)
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
		line("Index: %d collections, %d documents (%s)", len(colls), total, strings.Join(parts, ", "))
		convs, _ := c.hist.List(s.appCtx, 1_000_000)
		line("History: %d conversations", len(convs))
		vi, _ := migrate.Version(s.appCtx, c.st.DB(), "index")
		vh, _ := migrate.Version(s.appCtx, c.st.DB(), "history")
		line("Schema: index v%d, history v%d", vi, vh)
		if c.ix != nil {
			p := c.ix.Progress()
			line("Indexing: active=%v, paused=%v, %d/%d", p.Active, p.Paused, p.Done, p.Total)
		}
	}

	if s.crashText != "" {
		line("")
		line("--- Crash at %s ---", s.lastCrash)
		line("%s", redactHome(s.crashText))
	}
	if s.logs != nil {
		line("")
		line("--- Log (last 200 lines) ---")
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

func wailsVersion() string { return depVersion("github.com/wailsapp/wails/v3") }

// depVersion is the version of a module compiled into this binary.
// Pseudo-versions shrink to their commit ("v0.0.0-2026…-7c13302417b9" →
// "7c13302").
func depVersion(path string) string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == path {
				v := d.Version
				if d.Replace != nil {
					v = d.Replace.Version
				}
				if i := strings.LastIndexByte(v, '-'); i > 0 && strings.Count(v, "-") >= 2 && len(v)-i-1 == 12 {
					return v[i+1 : i+8]
				}
				return v
			}
		}
	}
	return "unknown"
}

// buildCommit is the commit the binary was built from: set by the build
// (-ldflags "-X main.commit=…"), else the VCS stamp of a plain go build.
func buildCommit() string {
	if commit != "" {
		return commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if rev != "" && dirty {
			rev += "+"
		}
		return rev
	}
	return ""
}

// AboutInfo is what the About box shows.
type AboutInfo struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"` // "" for a development build
	AskYourDocs string `json:"askyourdocs"`
	Wails       string `json:"wails"`
	Go          string `json:"go"`
	OS          string `json:"os"`   // darwin | windows | linux
	Arch        string `json:"arch"` // arm64 | amd64
	LLM         string `json:"llm"`  // "" = search only
	Embedding   string `json:"embedding"`
	Remote      string `json:"remote"` // host receiving excerpts, "" = all local
	DataDir     string `json:"dataDir"`
	Repository  string `json:"repository"`
}

func (s *AppService) About() AboutInfo {
	s.mu.RLock()
	cfg := s.settings
	s.mu.RUnlock()
	return AboutInfo{
		Version: version, Commit: buildCommit(),
		AskYourDocs: depVersion("github.com/dbis-ilm/askyourdocs"), Wails: wailsVersion(), Go: runtime.Version(),
		OS: runtime.GOOS, Arch: runtime.GOARCH,
		LLM: cfg.LLM, Embedding: cfg.Embedding, Remote: remoteHost(cfg),
		DataDir: displayPath(s.dataDir), Repository: repository,
	}
}

// OpenRepository opens the project page (or a page below it, e.g.
// "blob/main/LICENSE") in the default browser.
func (s *AppService) OpenRepository(page string) error {
	u := repository
	if page = strings.Trim(page, "/"); page != "" {
		if strings.Contains(page, "..") || strings.ContainsAny(page, "?#:") {
			return fmt.Errorf("invalid page %q", page)
		}
		u += "/" + page
	}
	return application.Get().Browser.OpenURL(u)
}

// ---- Agents (MCP) ----

// AgentServer returns the MCP server over the current index and engine; used
// by the --mcp mode.
func (s *AppService) AgentServer() (*agent.Server, error) {
	c, err := s.current()
	if err != nil {
		return nil, err
	}
	return agent.New(c.st, c.eng, version), nil
}

// MCPStatus describes how agents reach PersonalRAG: a client starts the app
// binary with --mcp and talks MCP over stdio.
func (s *AppService) MCPStatus() MCPStatus {
	st := MCPStatus{Running: true, Address: "stdio"}
	shared := 0
	if c, err := s.current(); err == nil {
		colls, _ := c.st.Collections(s.appCtx)
		for _, cl := range colls {
			if cl.SharedWithAgents {
				shared++
			}
		}
	}
	st.SharedCount = shared
	return st
}

func (s *AppService) ClientConfigs() []ClientConfig {
	exe, err := os.Executable()
	if err != nil {
		exe = "PersonalRAG"
	}
	return []ClientConfig{
		{
			Name: "OpenCode",
			Hint: i18n.T("client.opencode"),
			Snippet: fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "personalrag": {
      "type": "local",
      "command": [%q, "--mcp"],
      "enabled": true
    }
  }
}`, exe),
		},
		{
			Name: "Claude Desktop",
			Hint: i18n.T("client.claudeDesktop"),
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
			Hint:    i18n.T("client.claudeCode"),
			Snippet: fmt.Sprintf(`claude mcp add personalrag -- %q --mcp`, exe),
		},
		{
			Name: "Cursor",
			Hint: i18n.T("client.cursor"),
			Snippet: fmt.Sprintf(`{
  "mcpServers": {
    "personalrag": { "command": %q, "args": ["--mcp"] }
  }
}`, exe),
		},
	}
}

// AccessLog lists the newest agent tool calls.
func (s *AppService) AccessLog() []AccessLogEntry {
	out := []AccessLogEntry{}
	c, err := s.current()
	if err != nil {
		return out
	}
	entries, err := c.st.AccessLog(s.appCtx, 200)
	if err != nil {
		return out
	}
	for _, e := range entries {
		out = append(out, AccessLogEntry{At: e.At.UnixMilli(), Client: e.Client, Tool: e.Tool, Query: e.Request, Result: e.Result, Kind: e.Kind, DurationMs: e.Duration.Milliseconds()})
	}
	return out
}

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

func displayPath(p string) string { return paths.Display(p) }

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
