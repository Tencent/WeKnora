package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 000051_sessions_product_line 回归（customer-model-lines AC-2）：
// 存量库先停在 050，升级到 051 后旧行 product_line 默认空串、列可写；
// down 可回滚且数据行幸存。
func TestSQLiteSessionsProductLineUpgrade(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 50))
	dbPath := filepath.Join(t.TempDir(), "sessions-product-line.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	require.False(t, sqliteColumnExists(t, db, "sessions", "product_line"), "v50 must not have the column yet")
	_, err := db.Exec("INSERT INTO sessions (id, tenant_id) VALUES ('legacy', 1)")
	require.NoError(t, err)

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))

	require.True(t, sqliteColumnExists(t, db, "sessions", "product_line"))
	var line string
	require.NoError(t, db.QueryRow("SELECT product_line FROM sessions WHERE id = 'legacy'").Scan(&line))
	require.Empty(t, line, "legacy rows must default to empty string (history group)")
	_, err = db.Exec("UPDATE sessions SET product_line = 'kingdee-ai-suite' WHERE id = 'legacy'")
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT product_line FROM sessions WHERE id = 'legacy'").Scan(&line))
	require.Equal(t, "kingdee-ai-suite", line, "the column must be writable")

	// down 回滚：列移除、数据行幸存（直接执行 down SQL，与 000039 kind 先例同法）。
	down, err := os.ReadFile(filepath.Join(repoRoot, "migrations/sqlite/000051_sessions_product_line.down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	require.False(t, sqliteColumnExists(t, db, "sessions", "product_line"))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM sessions WHERE id = 'legacy'").Scan(&count))
	require.Equal(t, 1, count, "rows must survive the rollback")
}
