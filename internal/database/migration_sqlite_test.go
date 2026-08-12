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

func TestSQLiteMigrationUnifiesGraphEnabledState(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	dbPath := filepath.Join(t.TempDir(), "graph-migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// 模拟迁移执行前写入数据库的历史行。
	_, err = db.Exec(`
		INSERT INTO knowledge_bases (
			id, name, tenant_id, embedding_model_id, summary_model_id,
			extract_config, indexing_strategy
		) VALUES (
			'kb-legacy-graph', 'legacy graph', 1, 'embedding', 'summary',
			'{"enabled":true}',
			'{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'
		)`)
	require.NoError(t, err)

	// 测试数据库已经执行完全部迁移，因此单独再次执行该幂等迁移，模拟历史行升级。
	migrationSQL, err := os.ReadFile(filepath.Join(
		repoRoot,
		"migrations",
		"sqlite",
		"000004_unify_graph_enabled_state.up.sql",
	))
	require.NoError(t, err)
	_, err = db.Exec(string(migrationSQL))
	require.NoError(t, err)

	var graphEnabled int
	err = db.QueryRow(`
		SELECT json_extract(indexing_strategy, '$.graph_enabled')
		FROM knowledge_bases
		WHERE id = 'kb-legacy-graph'
	`).Scan(&graphEnabled)
	require.NoError(t, err)
	require.Equal(t, 1, graphEnabled)
}
