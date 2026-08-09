package database

import (
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeFileUpdateSlotMigratesExistingSQLite verifies the production
// auto-migration path for databases that already applied the upstream SQLite
// migrations before the update-slot table existed.
func TestKnowledgeFileUpdateSlotMigratesExistingSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY);
		CREATE TABLE schema_migrations (version INTEGER NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL);
		INSERT INTO schema_migrations(version, dirty) VALUES (2, 0);
	`)
	require.NoError(t, err)

	driver, err := sqlite3migrate.WithInstance(db, &sqlite3migrate.Config{})
	require.NoError(t, err)
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	migrator, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.Join(repoRoot, "migrations", "sqlite"), "sqlite3", driver,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = migrator.Close()
	})

	require.NoError(t, migrator.Up())
	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	require.Equal(t, uint(80), version)
	require.False(t, dirty)

	var tableName string
	err = db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name = 'knowledge_file_update_slots'
	`).Scan(&tableName)
	require.NoError(t, err)
	require.Equal(t, "knowledge_file_update_slots", tableName)
}
