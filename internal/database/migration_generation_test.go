package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/stretchr/testify/require"
)

func TestSQLiteGenerationSnapshotMigrationUpAndDown(t *testing.T) {
	repoRoot := findRepoRoot(t)
	previousWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { require.NoError(t, os.Chdir(previousWD)) })

	dbPath := filepath.Join(t.TempDir(), "weknora.sqlite")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://"+dbPath, MigrationOptions{
		SQLiteDBPath: dbPath,
	}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	require.True(t, sqliteColumnExists(t, db, "knowledges", "active_generation_id"))
	require.True(t, sqliteColumnExists(t, db, "chunks", "generation_id"))
	require.True(t, sqliteColumnExists(t, db, "chunks", "logical_chunk_key"))
	require.True(t, sqliteColumnExists(t, db, "chunks", "artifact_digest"))
	require.True(t, sqliteTableExists(t, db, "knowledge_generations"))
	require.True(t, sqliteTableExists(t, db, "processing_artifacts"))

	driver, err := sqlite3migrate.WithInstance(db, &sqlite3migrate.Config{})
	require.NoError(t, err)
	migrator, err := migrate.NewWithDatabaseInstance("file://"+filepath.ToSlash(filepath.Join(repoRoot, "migrations", "sqlite")), "sqlite3", driver)
	require.NoError(t, err)
	defer migrator.Close()
	require.NoError(t, migrator.Steps(-1))

	require.False(t, sqliteColumnExists(t, db, "knowledges", "active_generation_id"))
	require.False(t, sqliteColumnExists(t, db, "chunks", "generation_id"))
	require.False(t, sqliteColumnExists(t, db, "chunks", "logical_chunk_key"))
	require.False(t, sqliteColumnExists(t, db, "chunks", "artifact_digest"))
	require.False(t, sqliteTableExists(t, db, "knowledge_generations"))
	require.False(t, sqliteTableExists(t, db, "processing_artifacts"))
}

func TestGenerationSnapshotSchemaPresentInAllMaintainedSchemas(t *testing.T) {
	repoRoot := findRepoRoot(t)
	files := []string{
		filepath.Join("migrations", "versioned", "000079_generation_snapshot_artifacts.up.sql"),
		filepath.Join("migrations", "sqlite", "000002_generation_snapshot_artifacts.up.sql"),
		filepath.Join("migrations", "mysql", "00-init-db.sql"),
		filepath.Join("migrations", "paradedb", "00-init-db.sql"),
	}
	required := []string{
		"active_generation_id",
		"generation_id",
		"logical_chunk_key",
		"artifact_digest",
		"knowledge_generations",
		"processing_artifacts",
		"idx_knowledge_generations_lookup",
		"idx_chunks_active_generation",
		"uk_chunks_generation_logical",
		"artifact_key",
		"payload_checksum",
	}

	for _, rel := range files {
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
			require.NoError(t, err)
			sql := strings.ToLower(string(raw))
			for _, snippet := range required {
				require.Contains(t, sql, strings.ToLower(snippet))
			}
		})
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "go.mod not found")
		dir = parent
	}
}

func sqliteColumnExists(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + tableName + ")")
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk))
		if name == columnName {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}

func sqliteTableExists(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	require.NoError(t, err)
	return true
}
