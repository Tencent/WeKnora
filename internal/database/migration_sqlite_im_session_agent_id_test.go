package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Regression for Tencent/WeKnora#1066 on the SQLite path: the session lookup
// keys on agent_id, but the SQLite unique indexes did not include it, so
// switching a channel's agent failed with "UNIQUE constraint failed" and the
// incoming message was dropped before any reply was sent.
func TestSQLiteMigrationsIsolateChannelSessionsPerAgent(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 50))
	dbPath := filepath.Join(t.TempDir(), "im-agent-session.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)

	_, err := db.Exec(`INSERT INTO sessions (id, tenant_id) VALUES
		('sess-quick', 1), ('sess-reasoning', 1)`)
	require.NoError(t, err)

	const insert = `INSERT INTO im_channel_sessions
		(id, platform, user_id, chat_id, session_id, tenant_id, agent_id)
		VALUES (?, 'dingtalk', 'u1', '', ?, 1, ?)`
	_, err = db.Exec(insert, "cs-1", "sess-quick", "builtin-quick-answer")
	require.NoError(t, err)

	// Before the fix the old index rejected the second row for the same user.
	_, err = db.Exec(insert, "cs-2", "sess-reasoning", "builtin-smart-reasoning")
	require.Error(t, err)

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))

	var indexSQL string
	require.NoError(t, db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_channel_lookup'`,
	).Scan(&indexSQL))
	require.Contains(t, indexSQL, "agent_id")

	_, err = db.Exec(insert, "cs-2", "sess-reasoning", "builtin-smart-reasoning")
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM im_channel_sessions WHERE user_id = 'u1' AND deleted_at IS NULL`,
	).Scan(&count))
	require.Equal(t, 2, count)
}
