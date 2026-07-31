package database

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"
)

// sqliteMigrationsFS opens migrations/sqlite as an fs.FS. We deliberately avoid
// the file:// source driver here: on Windows the repo path may contain spaces
// and a drive letter, both of which a file:// URL mangles.
func sqliteMigrationsFS(t *testing.T) fs.FS {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// This file lives in internal/database, so the repo root is two levels up.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "migrations", "sqlite")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("migrations/sqlite not found at %s: %v", dir, err)
	}
	return os.DirFS(dir)
}

// newSQLiteMigrator builds a migrator over the real migrations/sqlite sequence
// against a fresh on-disk database. An on-disk file (not :memory:) is required
// because golang-migrate's sqlite3 driver opens its own handle.
func newSQLiteMigrator(t *testing.T) (*migrate.Migrate, *sql.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "weknora-test.db")
	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	if err != nil {
		t.Fatalf("sqlite3 migrate driver: %v", err)
	}

	src, err := iofs.New(sqliteMigrationsFS(t), ".")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	return m, sqlDB
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("query sqlite_master for %s: %v", name, err)
	}
	return n > 0
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("query sqlite_master for index %s: %v", name, err)
	}
	return n > 0
}

// columnDefault reports whether the column exists and, if so, its declared
// default expression as reported by PRAGMA table_info.
func columnDefault(t *testing.T, db *sql.DB, table, column string) (string, bool) {
	t.Helper()
	rows, err := db.Query(`SELECT name, dflt_value FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var dflt sql.NullString
		if err := rows.Scan(&name, &dflt); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		if name == column {
			return dflt.String, true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma rows: %v", err)
	}
	return "", false
}

// TestKnowledgeFolderSQLiteMigrationRoundTrip exercises the real
// migrations/sqlite sequence: full up, one step down, then up again. It guards
// the SQLite counterpart of migrations/versioned/000079_knowledge_folders,
// whose absence would leave SQLite deployments without the folders table.
func TestKnowledgeFolderSQLiteMigrationRoundTrip(t *testing.T) {
	m, db := newSQLiteMigrator(t)

	// --- full up -------------------------------------------------------
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}

	if !tableExists(t, db, "knowledge_folders") {
		t.Fatal("knowledge_folders table missing after up")
	}
	if _, ok := columnDefault(t, db, "knowledges", "folder_id"); !ok {
		t.Fatal("knowledges.folder_id missing after up")
	}
	for _, idx := range []string{
		"idx_knowledge_folders_parent_name",
		"idx_knowledge_folders_parent",
		"idx_knowledge_folders_deleted_at",
		"idx_knowledges_kb_folder",
	} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %s missing after up", idx)
		}
	}

	// A document row inserted without folder_id must land at the KB root.
	if _, err := db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, file_name)
		VALUES ('k-1', 1, 'kb-1', 'file', 'doc', 'manual', 'completed', 'doc.md')
	`); err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}
	var folderID string
	if err := db.QueryRow(`SELECT folder_id FROM knowledges WHERE id = 'k-1'`).Scan(&folderID); err != nil {
		t.Fatalf("read folder_id: %v", err)
	}
	if folderID != "" {
		t.Errorf("folder_id default = %q, want empty string (KB root)", folderID)
	}

	if _, err := db.Exec(`
		INSERT INTO knowledge_folders (id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
		VALUES ('f-1', 1, 'kb-1', '', 'specs', 'specs', 1)
	`); err != nil {
		t.Fatalf("insert folder: %v", err)
	}

	// A folder name is unique among live siblings under the same parent.
	if _, err := db.Exec(`
		INSERT INTO knowledge_folders (id, tenant_id, knowledge_base_id, parent_id, name, path, depth)
		VALUES ('f-2', 1, 'kb-1', '', 'specs', 'specs', 1)
	`); err == nil {
		t.Error("duplicate sibling folder name was accepted, unique index not enforced")
	}

	// --- one step down -------------------------------------------------
	if err := m.Steps(-1); err != nil {
		t.Fatalf("migrate down one step: %v", err)
	}

	if tableExists(t, db, "knowledge_folders") {
		t.Error("knowledge_folders table still present after down")
	}
	if _, ok := columnDefault(t, db, "knowledges", "folder_id"); ok {
		t.Error("knowledges.folder_id still present after down")
	}
	// The pre-existing document must survive the rollback.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM knowledges WHERE id = 'k-1'`).Scan(&count); err != nil {
		t.Fatalf("count knowledges after down: %v", err)
	}
	if count != 1 {
		t.Errorf("knowledges row count after down = %d, want 1", count)
	}

	// --- up again ------------------------------------------------------
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up after down: %v", err)
	}

	if !tableExists(t, db, "knowledge_folders") {
		t.Fatal("knowledge_folders table missing after re-up")
	}
	dflt, ok := columnDefault(t, db, "knowledges", "folder_id")
	if !ok {
		t.Fatal("knowledges.folder_id missing after re-up")
	}
	if dflt != "''" {
		t.Errorf("folder_id default expression = %q, want \"''\"", dflt)
	}
	// The surviving document is back at the KB root.
	if err := db.QueryRow(`SELECT folder_id FROM knowledges WHERE id = 'k-1'`).Scan(&folderID); err != nil {
		t.Fatalf("read folder_id after re-up: %v", err)
	}
	if folderID != "" {
		t.Errorf("folder_id after re-up = %q, want empty string", folderID)
	}
}
