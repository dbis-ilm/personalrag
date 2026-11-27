// Package history persists conversations: each turn keeps the question, the
// standalone question used for retrieval, a snapshot of the cited sources
// and the answer, so a conversation reopens exactly as it was answered even
// after the index changed.
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dbis-ilm/personalrag/internal/migrate"
)

// ErrNotFound is returned for unknown conversations or turns.
var ErrNotFound = errors.New("conversation not found")

type Conversation struct {
	ID           string
	Title        string
	CollectionID string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Turn struct {
	ID             int64
	ConversationID string
	Question       string
	Standalone     string // rewritten follow-up used for retrieval; "" if unchanged
	Mode           string // "answer" | "search"
	Sources        []byte // JSON snapshot, opaque to this package
	Answer         string
	Confidence     string
	Note           string
	Error          string
	Done           bool // answer finished (or not requested)
	CreatedAt      time.Time
}

type Store struct{ db *sql.DB }

// historySteps are the conversation schema's migrations; append only.
var historySteps = []migrate.Step{
	{Version: 1, Name: "baseline", Up: migrate.Exec(
		`CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, collection_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS conversations_updated ON conversations(updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS turns (
			id INTEGER PRIMARY KEY,
			conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			question TEXT NOT NULL, standalone TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL,
			sources TEXT NOT NULL DEFAULT '[]', answer TEXT NOT NULL DEFAULT '',
			confidence TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
			done INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS turns_conversation ON turns(conversation_id, id)`,
	)},
}

// New migrates the conversation tables in db (the index database). Pass the
// database file's path as backupPath's base to keep a copy before a real
// migration; empty skips the backup.
func New(db *sql.DB, backupPath string) (*Store, error) {
	if err := migrate.Apply(context.Background(), db, "history", historySteps, migrate.Options{BackupPath: backupPath}); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	return &Store{db: db}, nil
}

// Create starts a conversation titled after its first question.
func (s *Store) Create(ctx context.Context, id, firstQuestion, collectionID string) (Conversation, error) {
	now := time.Now()
	c := Conversation{ID: id, Title: Title(firstQuestion), CollectionID: collectionID, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(id, title, collection_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.CollectionID, now.UnixMilli(), now.UnixMilli())
	return c, err
}

// Title shortens a question to a list label.
func Title(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if utf8.RuneCountInString(q) <= 70 {
		return q
	}
	r := []rune(q)[:70]
	if i := strings.LastIndex(string(r), " "); i > 40 {
		return string(r)[:i] + " …"
	}
	return string(r) + " …"
}

func (s *Store) List(ctx context.Context, limit int) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, collection_id, created_at, updated_at
		FROM conversations ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.Title, &c.CollectionID, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (Conversation, []Turn, error) {
	var c Conversation
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id, title, collection_id, created_at, updated_at FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.CollectionID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil, ErrNotFound
	}
	if err != nil {
		return c, nil, err
	}
	c.CreatedAt, c.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	rows, err := s.db.QueryContext(ctx, `SELECT `+turnCols+` FROM turns WHERE conversation_id = ? ORDER BY id`, id)
	if err != nil {
		return c, nil, err
	}
	defer rows.Close()
	var turns []Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return c, nil, err
		}
		turns = append(turns, t)
	}
	return c, turns, rows.Err()
}

const turnCols = `id, conversation_id, question, standalone, mode, sources, answer, confidence, note, error, done, created_at`

func scanTurn(sc interface{ Scan(...any) error }) (Turn, error) {
	var t Turn
	var sources string
	var created int64
	err := sc.Scan(&t.ID, &t.ConversationID, &t.Question, &t.Standalone, &t.Mode, &sources, &t.Answer,
		&t.Confidence, &t.Note, &t.Error, &t.Done, &created)
	t.Sources, t.CreatedAt = []byte(sources), time.UnixMilli(created)
	return t, err
}

// RecentQuestions returns the questions asked, newest first, each once
// (by its latest use), across all conversations.
func (s *Store) RecentQuestions(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT question FROM turns
		GROUP BY question ORDER BY MAX(id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) Turn(ctx context.Context, id int64) (Turn, error) {
	t, err := scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnCols+` FROM turns WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// AddTurn appends a turn after retrieval; the answer follows via Finish.
func (s *Store) AddTurn(ctx context.Context, t Turn) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	upd, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, now, t.ConversationID)
	if err != nil {
		return 0, err
	}
	if n, _ := upd.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO turns(conversation_id, question, standalone, mode, sources, done, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ConversationID, t.Question, t.Standalone, t.Mode, string(t.Sources), t.Done, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

// SetSources replaces a turn's source snapshot, e.g. after "show more".
func (s *Store) SetSources(ctx context.Context, id int64, sources []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE turns SET sources = ? WHERE id = ?`, string(sources), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Finish stores the outcome of an answer.
func (s *Store) Finish(ctx context.Context, id int64, answer, confidence, note, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET answer = ?, confidence = ?, note = ?, error = ?, done = 1 WHERE id = ?`,
		answer, confidence, note, errText, id)
	return err
}

// MarkInterrupted closes answers that were still streaming when the app
// stopped, keeping whatever text had arrived.
func (s *Store) MarkInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET done = 1, error = '@interrupted' WHERE done = 0`)
	return err
}

// SavePartial stores streamed text so far, so an interruption keeps it.
func (s *Store) SavePartial(ctx context.Context, id int64, answer string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET answer = ? WHERE id = ? AND done = 0`, answer, id)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAll removes every conversation.
func (s *Store) DeleteAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations`)
	return err
}
