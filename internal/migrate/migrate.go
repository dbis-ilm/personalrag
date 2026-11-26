// Package migrate applies versioned schema migrations to the index database.
// Each component (index, history, …) keeps its own version in
// schema_versions, so packages own their tables without sharing one counter.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Step is one migration. Version numbers start at 1 and increase by one.
type Step struct {
	Version int
	Name    string
	Up      func(ctx context.Context, tx *sql.Tx) error
}

// ErrTooNew means the database was written by a newer app version.
var ErrTooNew = errors.New("Der Index stammt von einer neueren App-Version")

// Options tune Apply.
type Options struct {
	// BackupPath, if set, receives a copy of the database (VACUUM INTO)
	// before migrating a database that already has a schema. An existing
	// file at that path is replaced.
	BackupPath string
}

// Version returns the component's current schema version (0 = none yet).
func Version(ctx context.Context, db *sql.DB, component string) (int, error) {
	if err := ensureTable(ctx, db); err != nil {
		return 0, err
	}
	var v int
	err := db.QueryRowContext(ctx, `SELECT version FROM schema_versions WHERE component = ?`, component).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// Apply runs every step newer than the component's version, each in its own
// transaction together with the version bump. It refuses a database whose
// version is newer than the last step.
func Apply(ctx context.Context, db *sql.DB, component string, steps []Step, opts Options) error {
	for i, s := range steps {
		if s.Version != i+1 {
			return fmt.Errorf("migrate %s: step %d has version %d", component, i+1, s.Version)
		}
	}
	current, err := Version(ctx, db, component)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", component, err)
	}
	latest := len(steps)
	if current > latest {
		return fmt.Errorf("%w (%s: Version %d, diese App kennt %d)", ErrTooNew, component, current, latest)
	}
	if current == latest {
		return nil
	}
	if current > 0 && opts.BackupPath != "" {
		if err := backup(ctx, db, opts.BackupPath); err != nil {
			return fmt.Errorf("migrate %s: Sicherung vor der Migration: %w", component, err)
		}
		slog.Info("schema backup written", "component", component, "path", opts.BackupPath)
	}
	for _, s := range steps[current:] {
		if err := run(ctx, db, component, s); err != nil {
			return fmt.Errorf("migrate %s to version %d (%s): %w", component, s.Version, s.Name, err)
		}
		slog.Info("schema migrated", "component", component, "version", s.Version, "step", s.Name)
	}
	return nil
}

func run(ctx context.Context, db *sql.DB, component string, s Step) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.Up(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_versions(component, version) VALUES (?, ?)
		ON CONFLICT(component) DO UPDATE SET version = excluded.version`, component, s.Version); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_versions (
		component TEXT PRIMARY KEY, version INTEGER NOT NULL)`)
	return err
}

func backup(ctx context.Context, db *sql.DB, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// Exec returns a step body that runs statements in order.
func Exec(stmts ...string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}
}
