// Package store is the embedded index of PersonalRAG: one SQLite file holding
// documents, chunks, an FTS5 full-text index and a sqlite-vec vector index.
// It is CGo-free (modernc.org/sqlite + modernc.org/sqlite/vec).
//
// It satisfies askyourdocs.VectorStore, so askyourdocs.IndexSource can write
// into it directly. Nothing here is specific to the app; it is meant to move
// into askyourdocs as SqliteStore once it has settled.
package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/firebase/genkit/go/ai"

	"github.com/dbis-ilm/askyourdocs"
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/dbis-ilm/personalrag/internal/migrate"
	"github.com/dbis-ilm/personalrag/internal/paths"
)

// Metadata keys a chunk document carries into Index.
const (
	MetaDocumentID   = "documentId"
	MetaCollectionID = "collectionId"
	MetaParentText   = "parentText"
	MetaPages        = "pages"
	MetaSection      = "section"
	MetaBreadcrumb   = "breadcrumb"
	MetaChunkID      = "chunkId"
	MetaPath         = "url"
)

// Document statuses.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusError   = "error"
	StatusOCR     = "ocr"
	StatusCloud   = "cloud"
)

// ErrEmbeddingMismatch means the index was built with another embedding
// model or dimension. Vectors of different models don't compare, so the
// caller has to reset the vectors (ResetVectors) and re-index.
var ErrEmbeddingMismatch = errors.New("index was built with a different embedding model")

// openStores counts stores opened and not yet closed, so tests can prove
// that nothing keeps the index open.
var openStores atomic.Int64

// OpenCount returns how many stores are open in this process.
func OpenCount() int64 { return openStores.Load() }

type Store struct {
	closed atomic.Bool
	db     *sql.DB
	emb    ai.Embedder
	model  string
	dim    int
}

type Collection struct {
	ID               string
	Name             string
	Color            string
	SharedWithAgents bool
	Folders          []string
	// Instructions are the user's notes for answers from this collection
	// (style, terms); they never override the prompts' rules.
	Instructions string
	DocCount     int
}

type Document struct {
	ID           int64
	Path         string
	CollectionID string
	Size         int64
	ModTime      int64
	Hash         string
	Format       string
	Status       string
	Note         string
	Chunks       int
	IndexedAt    int64
}

// Hit is one retrieved chunk with its document.
type Hit struct {
	ChunkID      int64
	DocumentID   int64
	Path         string
	CollectionID string
	Text         string
	ParentText   string
	Pages        string
	Section      string
	Breadcrumb   string
	Score        float64 // vector: cosine similarity; keyword: -bm25 (higher is better)
}

// Open opens (or creates) the index at path. emb may be nil for a reader that
// only uses keyword search and administration. model and dim identify the
// embedding model; a mismatch with an existing index returns the store
// together with ErrEmbeddingMismatch, so the caller can decide to reset.
func Open(path string, emb ai.Embedder, model string, dim int) (*Store, error) {
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(10000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, emb: emb, model: model, dim: dim}
	if err := s.migrate(path); err != nil {
		db.Close()
		return nil, err
	}
	openStores.Add(1)
	if err := s.checkEmbedding(); err != nil {
		return s, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		openStores.Add(-1)
	}
	return s.db.Close()
}

// indexSteps are the index schema's migrations. Step 1 is idempotent so it
// adopts databases created before versioning; later steps must not be
// edited once released, only appended.
var indexSteps = []migrate.Step{
	{Version: 1, Name: "baseline", Up: migrate.Exec(
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS collections (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, color TEXT NOT NULL,
			shared_agents INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS folders (
			path TEXT PRIMARY KEY,
			collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE,
			collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
			size INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL DEFAULT 0, hash TEXT NOT NULL DEFAULT '',
			format TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
			chunks INTEGER NOT NULL DEFAULT 0, indexed_at INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS documents_collection ON documents(collection_id)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY,
			document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
			text TEXT NOT NULL, parent_text TEXT NOT NULL DEFAULT '', pages TEXT NOT NULL DEFAULT '',
			section TEXT NOT NULL DEFAULT '', breadcrumb TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS chunks_document ON chunks(document_id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(text, section, tokenize = 'unicode61 remove_diacritics 2')`,
		`DELETE FROM meta WHERE key = 'schema_version'`,
	)},
	{Version: 2, Name: "agent access log", Up: migrate.Exec(
		`CREATE TABLE IF NOT EXISTS agent_log (
			id INTEGER PRIMARY KEY, at INTEGER NOT NULL, client TEXT NOT NULL DEFAULT '',
			tool TEXT NOT NULL, request TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'ok')`,
		`CREATE INDEX IF NOT EXISTS agent_log_at ON agent_log(at DESC)`,
	)},
	{Version: 3, Name: "extract cache, call duration", Up: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS extract_cache (
			key TEXT PRIMARY KEY, document_id INTEGER NOT NULL, result TEXT NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS extract_cache_document ON extract_cache(document_id)`); err != nil {
			return err
		}
		// The app and an MCP process may migrate at the same time; add the
		// column only if the other one has not.
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('agent_log') WHERE name = 'duration_ms'`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			_, err := tx.ExecContext(ctx, `ALTER TABLE agent_log ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0`)
			return err
		}
		return nil
	}},
	// The PDF reader now keeps columns apart (askyourdocs#29, #30): lists set side
	// by side used to come out glued line by line. Indexed PDFs are queued
	// again so their chunks get the new text; cached extract results for
	// them were computed on the old text. A later change to text extraction
	// adds a step like this one.
	{Version: 4, Name: "re-index PDFs: columns kept apart", Up: reindexPDFs},
	{Version: 5, Name: "collection instructions", Up: func(ctx context.Context, tx *sql.Tx) error {
		// The app and an MCP process may migrate at the same time.
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('collections') WHERE name = 'instructions'`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			_, err := tx.ExecContext(ctx, `ALTER TABLE collections ADD COLUMN instructions TEXT NOT NULL DEFAULT ''`)
			return err
		}
		return nil
	}},
	// askyourdocs#31, #32: a space between text objects on one line
	// (letterhead name and date), page-count footers dropped from two-page
	// documents, page breaks kept after footers of appended slide decks.
	{Version: 6, Name: "re-index PDFs: spacing and footers", Up: reindexPDFs},
}

// reindexPDFs queues indexed PDFs again and drops their cached extract
// results, for a change in PDF text extraction.
var reindexPDFs = migrate.Exec(
	`DELETE FROM extract_cache WHERE document_id IN (SELECT id FROM documents WHERE format = 'PDF')`,
	`UPDATE documents SET status = 'pending' WHERE format = 'PDF' AND status = 'ok'`,
)

func (s *Store) migrate(path string) error {
	return migrate.Apply(context.Background(), s.db, "index", indexSteps, migrate.Options{BackupPath: path + ".bak"})
}

// checkEmbedding creates the vector table on first use and verifies the
// recorded model/dimension afterwards.
func (s *Store) checkEmbedding() error {
	if s.dim <= 0 {
		return nil
	}
	var model, dim string
	_ = s.db.QueryRow(`SELECT value FROM meta WHERE key = 'embedding_model'`).Scan(&model)
	_ = s.db.QueryRow(`SELECT value FROM meta WHERE key = 'embedding_dim'`).Scan(&dim)
	if model == "" {
		return s.createVectorTable()
	}
	if model != s.model || dim != strconv.Itoa(s.dim) {
		return fmt.Errorf("%w: index %s/%s, configured %s/%d", ErrEmbeddingMismatch, model, dim, s.model, s.dim)
	}
	return nil
}

func (s *Store) createVectorTable() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_vec USING vec0(
		collection_id TEXT, embedding float[%d] distance_metric=cosine)`, s.dim)
	if _, err := tx.Exec(q); err != nil {
		return fmt.Errorf("create vector table: %w", err)
	}
	for k, v := range map[string]string{"embedding_model": s.model, "embedding_dim": strconv.Itoa(s.dim)} {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResetVectors drops all chunks and vectors and marks every document for
// re-indexing, then recreates the vector table for the configured model.
func (s *Store) ResetVectors() error {
	stmts := []string{
		`DROP TABLE IF EXISTS chunks_vec`,
		`DELETE FROM chunks_fts`,
		`DELETE FROM chunks`,
		`UPDATE documents SET status = 'pending', hash = '', chunks = 0`,
		`DELETE FROM meta WHERE key IN ('embedding_model', 'embedding_dim')`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return s.createVectorTable()
}

// ---- askyourdocs.VectorStore ----

var _ askyourdocs.VectorStore = (*Store)(nil)

// Index embeds docs and stores them. Each document must carry
// MetaDocumentID and MetaCollectionID.
func (s *Store) Index(ctx context.Context, docs []*ai.Document) error {
	if len(docs) == 0 {
		return nil
	}
	if s.emb == nil {
		return errors.New("store has no embedder")
	}
	resp, err := s.emb.Embed(ctx, &ai.EmbedRequest{Input: docs})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(resp.Embeddings) != len(docs) {
		return fmt.Errorf("embed: got %d embeddings for %d documents", len(resp.Embeddings), len(docs))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	counts := map[int64]int{}
	for i, d := range docs {
		docID, ok := toInt64(d.Metadata[MetaDocumentID])
		if !ok {
			return fmt.Errorf("doc %d: missing %s", i, MetaDocumentID)
		}
		collID, _ := d.Metadata[MetaCollectionID].(string)
		vec := resp.Embeddings[i].Embedding
		if len(vec) != s.dim {
			return fmt.Errorf("doc %d: embedding has %d dimensions, index expects %d", i, len(vec), s.dim)
		}
		text := askyourdocs.DocText(d)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO chunks(document_id, text, parent_text, pages, section, breadcrumb) VALUES (?, ?, ?, ?, ?, ?)`,
			docID, text, str(d.Metadata, MetaParentText), str(d.Metadata, MetaPages),
			str(d.Metadata, MetaSection), str(d.Metadata, MetaBreadcrumb))
		if err != nil {
			return fmt.Errorf("insert chunk: %w", err)
		}
		id, _ := res.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks_fts(rowid, text, section) VALUES (?, ?, ?)`,
			id, text, str(d.Metadata, MetaSection)); err != nil {
			return fmt.Errorf("insert fts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks_vec(rowid, collection_id, embedding) VALUES (?, ?, ?)`,
			id, collID, float32Blob(vec)); err != nil {
			return fmt.Errorf("insert vector: %w", err)
		}
		counts[docID]++
	}
	for id, n := range counts {
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET chunks = chunks + ? WHERE id = ?`, n, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteByURL removes all chunks of the document at path (url = file path).
func (s *Store) DeleteByURL(ctx context.Context, path string) (int, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM documents WHERE path = ?`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return s.DeleteChunks(ctx, id)
}

// DeleteBySource is DeleteByURL: a local file's source name is its path.
func (s *Store) DeleteBySource(ctx context.Context, source string) (int, error) {
	return s.DeleteByURL(ctx, source)
}

// DeleteChunks removes a document's chunks from all three tables.
func (s *Store) DeleteChunks(ctx context.Context, documentID int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM extract_cache WHERE document_id = ?`, documentID); err != nil {
		return 0, err
	}
	sub := `(SELECT id FROM chunks WHERE document_id = ?)`
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE rowid IN `+sub, documentID); err != nil {
		return 0, err
	}
	if s.hasVectorTable(ctx, tx) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_vec WHERE rowid IN `+sub, documentID); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE document_id = ?`, documentID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE documents SET chunks = 0 WHERE id = ?`, documentID); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

func (s *Store) hasVectorTable(ctx context.Context, tx *sql.Tx) bool {
	var n int
	_ = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'chunks_vec'`).Scan(&n)
	return n > 0
}

// ---- Retrieval ----

// Retrieve is a k-nearest-neighbour search over chunk embeddings, optionally
// limited to one collection (empty = all).
func (s *Store) Retrieve(ctx context.Context, query string, k int, collectionID string) ([]Hit, error) {
	if s.emb == nil {
		return nil, errors.New("store has no embedder")
	}
	resp, err := s.emb.Embed(ctx, &ai.EmbedRequest{Input: []*ai.Document{askyourdocs.QueryDocument(query)}})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(resp.Embeddings) != 1 {
		return nil, errors.New("embed query: no embedding returned")
	}
	knn := `SELECT rowid, distance FROM chunks_vec WHERE embedding MATCH ? AND k = ?`
	args := []any{float32Blob(resp.Embeddings[0].Embedding), k}
	if collectionID != "" {
		knn += ` AND collection_id = ?`
		args = append(args, collectionID)
	}
	q := `WITH knn AS (` + knn + `)
		SELECT c.id, c.document_id, d.path, d.collection_id, c.text, c.parent_text, c.pages, c.section, c.breadcrumb, 1 - knn.distance
		FROM knn JOIN chunks c ON c.id = knn.rowid JOIN documents d ON d.id = c.document_id
		ORDER BY knn.distance`
	return s.queryHits(ctx, q, args...)
}

// KeywordSearch runs an FTS5 query built from the question's keywords
// (prefix matches, OR-combined, ranked by BM25).
func (s *Store) KeywordSearch(ctx context.Context, query string, k int, collectionID string, stopWords map[string]bool) ([]Hit, error) {
	match := FTSQuery(query, stopWords)
	if match == "" {
		return nil, nil
	}
	q := `SELECT c.id, c.document_id, d.path, d.collection_id, c.text, c.parent_text, c.pages, c.section, c.breadcrumb, -bm25(chunks_fts)
		FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid JOIN documents d ON d.id = c.document_id
		WHERE chunks_fts MATCH ?`
	args := []any{match}
	if collectionID != "" {
		q += ` AND d.collection_id = ?`
		args = append(args, collectionID)
	}
	q += ` ORDER BY bm25(chunks_fts) LIMIT ?`
	args = append(args, k)
	return s.queryHits(ctx, q, args...)
}

// FTSQuery turns free text into an FTS5 expression: quoted phrases stay
// phrases, keywords of four or more characters become prefix terms.
func FTSQuery(text string, stopWords map[string]bool) string {
	var terms []string
	for _, p := range askyourdocs.ExtractQuotedPhrases(text) {
		terms = append(terms, `"`+strings.ReplaceAll(p, `"`, ``)+`"`)
	}
	for _, w := range askyourdocs.ExtractKeywords(text, stopWords) {
		t := `"` + strings.ReplaceAll(w, `"`, ``) + `"`
		if len([]rune(w)) >= 4 {
			t += "*"
		}
		terms = append(terms, t)
	}
	return strings.Join(terms, " OR ")
}

func (s *Store) queryHits(ctx context.Context, q string, args ...any) ([]Hit, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ChunkID, &h.DocumentID, &h.Path, &h.CollectionID, &h.Text, &h.ParentText,
			&h.Pages, &h.Section, &h.Breadcrumb, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Chunk returns one chunk with its document.
func (s *Store) Chunk(ctx context.Context, id int64) (Hit, error) {
	hits, err := s.queryHits(ctx, `SELECT c.id, c.document_id, d.path, d.collection_id, c.text, c.parent_text, c.pages, c.section, c.breadcrumb, 0
		FROM chunks c JOIN documents d ON d.id = c.document_id WHERE c.id = ?`, id)
	if err != nil {
		return Hit{}, err
	}
	if len(hits) == 0 {
		return Hit{}, sql.ErrNoRows
	}
	return hits[0], nil
}

// ---- Collections ----

func (s *Store) Collections(ctx context.Context) ([]Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.name, c.color, c.shared_agents, c.instructions,
		(SELECT count(*) FROM documents d WHERE d.collection_id = c.id)
		FROM collections c ORDER BY c.created_at`)
	if err != nil {
		return nil, err
	}
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &c.SharedWithAgents, &c.Instructions, &c.DocCount); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Folders, err = s.folders(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) folders(ctx context.Context, collectionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM folders WHERE collection_id = ? ORDER BY path`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateCollection(ctx context.Context, c Collection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO collections(id, name, color, shared_agents, instructions, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Color, c.SharedWithAgents, c.Instructions, time.Now().UnixNano()); err != nil {
		return err
	}
	for _, f := range c.Folders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO folders(path, collection_id) VALUES (?, ?)`, f, c.ID); err != nil {
			return fmt.Errorf("ordner %s: %w", f, err)
		}
	}
	return tx.Commit()
}

// SetCollectionInstructions stores the user's notes for a collection.
func (s *Store) SetCollectionInstructions(ctx context.Context, id, text string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE collections SET instructions = ? WHERE id = ?`, text, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("unknown collection %q", id)
	}
	return nil
}

// CollectionInstructions returns the user's notes for a collection; "" for
// none or an unknown collection.
func (s *Store) CollectionInstructions(ctx context.Context, id string) string {
	var text string
	_ = s.db.QueryRowContext(ctx, `SELECT instructions FROM collections WHERE id = ?`, id).Scan(&text)
	return text
}

func (s *Store) SetCollectionShared(ctx context.Context, id string, shared bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE collections SET shared_agents = ? WHERE id = ?`, shared, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("unknown collection %q", id)
	}
	return nil
}

// DeleteCollection removes a collection with its documents, chunks and vectors.
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	docs, err := s.Documents(ctx, id, "")
	if err != nil {
		return err
	}
	for _, d := range docs {
		if _, err := s.DeleteChunks(ctx, d.ID); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	return err
}

// FolderOwner is a watched folder and the collection it belongs to.
type FolderOwner struct {
	Path         string
	CollectionID string
}

// AllFolders lists every watched folder.
func (s *Store) AllFolders(ctx context.Context) ([]FolderOwner, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, collection_id FROM folders`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderOwner
	for rows.Next() {
		var f FolderOwner
		if err := rows.Scan(&f.Path, &f.CollectionID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CollectionForPath returns the collection whose folder contains path,
// comparing paths the way the platform does (case-insensitive on Windows).
// The innermost folder wins.
func (s *Store) CollectionForPath(ctx context.Context, path string) (string, bool, error) {
	folders, err := s.AllFolders(ctx)
	if err != nil {
		return "", false, err
	}
	best, bestLen := "", -1
	for _, f := range folders {
		if paths.Within(path, f.Path) && len(f.Path) > bestLen {
			best, bestLen = f.CollectionID, len(f.Path)
		}
	}
	return best, bestLen >= 0, nil
}

// ---- Documents ----

const docCols = `id, path, collection_id, size, mtime, hash, format, status, note, chunks, indexed_at`

func scanDoc(sc interface{ Scan(...any) error }) (Document, error) {
	var d Document
	err := sc.Scan(&d.ID, &d.Path, &d.CollectionID, &d.Size, &d.ModTime, &d.Hash, &d.Format, &d.Status, &d.Note, &d.Chunks, &d.IndexedAt)
	return d, err
}

// Documents lists documents, optionally filtered by collection and status.
func (s *Store) Documents(ctx context.Context, collectionID, status string) ([]Document, error) {
	q := `SELECT ` + docCols + ` FROM documents WHERE 1 = 1`
	var args []any
	if collectionID != "" {
		q += ` AND collection_id = ?`
		args = append(args, collectionID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY path`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DocumentByPath(ctx context.Context, path string) (Document, bool, error) {
	d, err := scanDoc(s.db.QueryRowContext(ctx, `SELECT `+docCols+` FROM documents WHERE path = ?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, false, nil
	}
	return d, err == nil, err
}

func (s *Store) DocumentByID(ctx context.Context, id int64) (Document, error) {
	return scanDoc(s.db.QueryRowContext(ctx, `SELECT `+docCols+` FROM documents WHERE id = ?`, id))
}

// UpsertDocument inserts or updates the row for d.Path and returns its ID.
func (s *Store) UpsertDocument(ctx context.Context, d Document) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO documents(path, collection_id, size, mtime, hash, format, status, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET collection_id = excluded.collection_id, size = excluded.size,
			mtime = excluded.mtime, format = excluded.format, status = excluded.status, note = excluded.note
		RETURNING id`,
		d.Path, d.CollectionID, d.Size, d.ModTime, d.Hash, d.Format, d.Status, d.Note).Scan(&id)
	return id, err
}

// SetDocumentState records the outcome of an indexing attempt. hash is only
// written on success, so a failed file is retried on the next scan.
func (s *Store) SetDocumentState(ctx context.Context, id int64, status, note, hash string) error {
	if status == StatusOK {
		_, err := s.db.ExecContext(ctx, `UPDATE documents SET status = ?, note = ?, hash = ?, indexed_at = ? WHERE id = ?`,
			status, note, hash, time.Now().Unix(), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE documents SET status = ?, note = ? WHERE id = ?`, status, note, id)
	return err
}

// TouchDocument updates size/mtime when the content hash is unchanged.
func (s *Store) TouchDocument(ctx context.Context, id, size, mtime int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE documents SET size = ?, mtime = ? WHERE id = ?`, size, mtime, id)
	return err
}

// DeleteDocument removes a document and its chunks from the index.
func (s *Store) DeleteDocument(ctx context.Context, id int64) error {
	if _, err := s.DeleteChunks(ctx, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	return err
}

// StatusCounts returns the number of documents per status.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, count(*) FROM documents GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ---- helpers ----

func float32Blob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// ---- Agent access log ----

// AccessEntry is one tool call by an agent (MCP client).
type AccessEntry struct {
	At       time.Time
	Client   string
	Tool     string
	Request  string
	Result   string
	Kind     string // ok | warn | denied | error
	Duration time.Duration
}

// maxAccessLog caps the log; older entries are dropped.
const maxAccessLog = 5000

// LogAccess records an agent's tool call.
func (s *Store) LogAccess(ctx context.Context, e AccessEntry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO agent_log(at, client, tool, request, result, kind, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.At.UnixMilli(), e.Client, e.Tool, e.Request, e.Result, e.Kind, e.Duration.Milliseconds()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_log WHERE id <= (SELECT max(id) FROM agent_log) - ?`, maxAccessLog)
	return err
}

// AccessLog returns the newest entries first.
func (s *Store) AccessLog(ctx context.Context, limit int) ([]AccessEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, client, tool, request, result, kind, duration_ms FROM agent_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessEntry
	for rows.Next() {
		var e AccessEntry
		var at, ms int64
		if err := rows.Scan(&at, &e.Client, &e.Tool, &e.Request, &e.Result, &e.Kind, &ms); err != nil {
			return nil, err
		}
		e.At, e.Duration = time.UnixMilli(at), time.Duration(ms)*time.Millisecond
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Extract cache ----

// maxExtractCache caps cached extract results; older ones are dropped.
const maxExtractCache = 20000

// CachedExtract returns a stored extract result.
func (s *Store) CachedExtract(ctx context.Context, key string) (string, bool, error) {
	var r string
	err := s.db.QueryRowContext(ctx, `SELECT result FROM extract_cache WHERE key = ?`, key).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return r, err == nil, err
}

// PutExtract stores an extract result for one document part. Results go
// when the document is re-indexed or removed.
func (s *Store) PutExtract(ctx context.Context, key string, documentID int64, result string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO extract_cache(key, document_id, result, created_at) VALUES (?, ?, ?, ?)`,
		key, documentID, result, time.Now().Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM extract_cache WHERE rowid <= (SELECT max(rowid) FROM extract_cache) - ?`, maxExtractCache)
	return err
}
