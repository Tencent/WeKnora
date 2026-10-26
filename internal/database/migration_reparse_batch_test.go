package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteReparseBatchMigrationRollbackPreservesBusinessRows(t *testing.T) {
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	path := filepath.Join(t.TempDir(), "reparse.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: path}))
	db := openSQLiteDB(t, path)
	_, err := db.Exec(`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status)
		VALUES ('synthetic-document', 1, 'synthetic-kb', 'manual', 'Synthetic', 'manual', 'failed')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO task_pending_ops (tenant_id, task_type, scope, scope_id, op)
		VALUES (1, 'synthetic-task', 'knowledge', 'synthetic-document', 'enqueue')`)
	require.NoError(t, err)

	down, err := os.ReadFile(filepath.Join(root, "migrations/sqlite/000051_reparse_batches.down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	for _, table := range []string{"reparse_batches", "reparse_batch_members", "reparse_batch_submissions"} {
		require.False(t, sqliteTableExists(t, db, table))
	}
	up, err := os.ReadFile(filepath.Join(root, "migrations/sqlite/000051_reparse_batches.up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	// The DDL is restart-safe and contains no business or task updates.
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	for _, table := range []string{"reparse_batches", "reparse_batch_members", "reparse_batch_submissions"} {
		require.True(t, sqliteTableExists(t, db, table))
	}
	var status string
	require.NoError(t, db.QueryRow(`SELECT parse_status FROM knowledges WHERE id = 'synthetic-document'`).Scan(&status))
	require.Equal(t, "failed", status)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM task_pending_ops`).Scan(&count))
	require.Equal(t, 1, count)
}
