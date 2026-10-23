package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteModelDefaultsUpgradePreservesLegacySelection(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 50))
	dbPath := filepath.Join(t.TempDir(), "model-defaults.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	want := seedModelDefaultHistory(t, db)
	before := modelDefaultHistorySnapshot(t, db)
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	require.Equal(t, before, modelDefaultHistorySnapshot(t, db), "migration must preserve model rows and credentials")
	migrationPath := filepath.Join(repoRoot, "migrations/sqlite/000051_model_defaults.up.sql")
	assertMigratedModelDefaults(t, db, "sqlite", migrationPath, want)
}
