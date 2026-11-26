package migrate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func open(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var v1 = Step{Version: 1, Name: "baseline", Up: Exec(`CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, text TEXT NOT NULL)`)}
var v2 = Step{Version: 2, Name: "add pinned", Up: Exec(`ALTER TABLE notes ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`)}

func TestApplyFreshAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "a.db"))
	if err := Apply(ctx, db, "notes", []Step{v1, v2}, Options{}); err != nil {
		t.Fatal(err)
	}
	if v, _ := Version(ctx, db, "notes"); v != 2 {
		t.Fatalf("version = %d", v)
	}
	if err := Apply(ctx, db, "notes", []Step{v1, v2}, Options{}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes(text, pinned) VALUES ('x', 1)`); err != nil {
		t.Fatal(err)
	}
}

// A database created before versioning (tables exist, no schema_versions)
// is adopted by the idempotent baseline, keeping its rows, and backed up
// before the real migration runs.
func TestAdoptLegacyWithBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := open(t, filepath.Join(dir, "legacy.db"))
	db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT NOT NULL)`)
	db.Exec(`INSERT INTO notes(text) VALUES ('kept')`)

	if err := Apply(ctx, db, "notes", []Step{v1}, Options{}); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(dir, "legacy.db.bak")
	if err := Apply(ctx, db, "notes", []Step{v1, v2}, Options{BackupPath: bak}); err != nil {
		t.Fatal(err)
	}
	var text string
	var pinned int
	if err := db.QueryRow(`SELECT text, pinned FROM notes`).Scan(&text, &pinned); err != nil || text != "kept" {
		t.Fatalf("row lost: %q %v", text, err)
	}
	bdb := open(t, bak)
	if err := bdb.QueryRow(`SELECT text FROM notes`).Scan(&text); err != nil || text != "kept" {
		t.Fatalf("backup unusable: %v", err)
	}
	if _, err := bdb.Exec(`SELECT pinned FROM notes`); err == nil {
		t.Fatal("backup should hold the pre-migration schema")
	}
}

func TestFailedStepRollsBack(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "a.db"))
	bad := Step{Version: 2, Name: "broken", Up: Exec(
		`ALTER TABLE notes ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`,
		`THIS IS NOT SQL`,
	)}
	if err := Apply(ctx, db, "notes", []Step{v1, bad}, Options{}); err == nil {
		t.Fatal("broken step must fail")
	}
	if v, _ := Version(ctx, db, "notes"); v != 1 {
		t.Fatalf("version after failure = %d, want 1", v)
	}
	if _, err := db.Exec(`SELECT pinned FROM notes`); err == nil {
		t.Fatal("partial step was not rolled back")
	}
	// Fixed step applies afterwards.
	if err := Apply(ctx, db, "notes", []Step{v1, v2}, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "a.db"))
	if err := Apply(ctx, db, "notes", []Step{v1, v2}, Options{}); err != nil {
		t.Fatal(err)
	}
	err := Apply(ctx, db, "notes", []Step{v1}, Options{})
	if !errors.Is(err, ErrTooNew) {
		t.Fatalf("older app on newer DB: %v", err)
	}
}

func TestComponentsAreIndependent(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "a.db"))
	other := Step{Version: 1, Name: "x", Up: Exec(`CREATE TABLE IF NOT EXISTS other (id INTEGER)`)}
	if err := Apply(ctx, db, "notes", []Step{v1, v2}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, "other", []Step{other}, Options{}); err != nil {
		t.Fatal(err)
	}
	if v, _ := Version(ctx, db, "other"); v != 1 {
		t.Fatalf("other = %d", v)
	}
	if v, _ := Version(ctx, db, "notes"); v != 2 {
		t.Fatalf("notes changed by another component: %d", v)
	}
}
