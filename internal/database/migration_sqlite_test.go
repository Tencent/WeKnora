package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationsIncludeAutoTagConfig(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows, err := db.Query("PRAGMA table_info(knowledge_bases)")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		if name == "auto_tag_config" {
			found = true
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, found, "SQLite migrations must create knowledge_bases.auto_tag_config")
}

func TestSQLiteMigrationsIncludeRuntimeSettingsAndTaskQueue(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, table := range []string{"system_settings", "task_pending_ops", "task_dead_letters"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		require.NoError(t, err, "SQLite migrations must create %s", table)
		require.Equal(t, table, name)
	}

	var isSystemAdmin int
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'is_system_admin'").Scan(&isSystemAdmin)
	require.NoError(t, err)
	require.Equal(t, 1, isSystemAdmin, "SQLite migrations must add users.is_system_admin")

	var pendingSubtasks int
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('knowledges') WHERE name = 'pending_subtasks_count'").Scan(&pendingSubtasks)
	require.NoError(t, err)
	require.Equal(t, 1, pendingSubtasks, "SQLite migrations must add knowledges.pending_subtasks_count")

	var apiPrincipalConfig int
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('tenants') WHERE name = 'api_principal_config'").Scan(&apiPrincipalConfig)
	require.NoError(t, err)
	require.Equal(t, 1, apiPrincipalConfig, "SQLite migrations must add tenants.api_principal_config")
}
