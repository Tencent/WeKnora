package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostgresIMSessionModeIndexes(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	// Temporary tables shadow any real tables. Roll back the whole fixture so
	// this test neither needs ParadeDB nor changes existing application data.
	_, err = tx.Exec(`CREATE TEMP TABLE im_channel_sessions (
		id TEXT PRIMARY KEY, platform TEXT NOT NULL, user_id TEXT NOT NULL,
		chat_id TEXT NOT NULL, tenant_id BIGINT NOT NULL, agent_id TEXT DEFAULT '',
		thread_id TEXT NOT NULL DEFAULT '', deleted_at TIMESTAMPTZ
	)`)
	require.NoError(t, err)
	root := sqliteRepoRoot(t)
	run := func(file string) error {
		script, err := os.ReadFile(filepath.Join(root, "migrations", "versioned", file))
		require.NoError(t, err)
		_, err = tx.Exec(string(script))
		return err
	}
	// Seed both old indexes before executing migration 38, so DROP INDEX
	// resolves to the temp schema even on a database with application tables.
	_, err = tx.Exec(`CREATE INDEX idx_channel_lookup ON im_channel_sessions (user_id);
		CREATE INDEX idx_channel_thread_lookup ON im_channel_sessions (thread_id)`)
	require.NoError(t, err)
	require.NoError(t, run("000038_im_session_agent_id.up.sql"))
	_, err = tx.Exec(`INSERT INTO im_channel_sessions VALUES
		('legacy', 'feishu', 'alice', 'group', 1, 'agent-1', '', NULL)`)
	require.NoError(t, err)
	require.NoError(t, run("000137_im_session_mode_indexes.up.sql"))
	_, err = tx.Exec(`INSERT INTO im_channel_sessions VALUES
		('topic-1', 'feishu', 'alice', 'group', 1, 'agent-1', 'thread-1', NULL),
		('topic-2', 'feishu', 'alice', 'group', 1, 'agent-1', 'thread-2', NULL),
		('agent-user', 'feishu', 'alice', 'group', 1, 'agent-2', '', NULL),
		('agent-thread', 'feishu', 'alice', 'group', 1, 'agent-2', 'thread-1', NULL),
		('deleted-user', 'feishu', 'alice', 'group', 1, 'agent-1', '', now()),
		('deleted-thread', 'feishu', 'alice', 'group', 1, 'agent-1', 'thread-1', now())`)
	require.NoError(t, err)
	for _, tc := range []struct{ user, thread, index string }{
		{"alice", "", "idx_channel_lookup"},
		{"bob", "thread-1", "idx_channel_thread_lookup"},
	} {
		_, err = tx.Exec("SAVEPOINT duplicate_mapping")
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO im_channel_sessions VALUES
			('duplicate', 'feishu', $1, 'group', 1, 'agent-1', $2, NULL)`, tc.user, tc.thread)
		require.ErrorContains(t, err, tc.index)
		_, err = tx.Exec("ROLLBACK TO SAVEPOINT duplicate_mapping")
		require.NoError(t, err)
	}
	_, err = tx.Exec("SAVEPOINT incompatible_down")
	require.NoError(t, err)
	require.ErrorContains(t, run("000137_im_session_mode_indexes.down.sql"), "idx_channel_lookup")
	_, err = tx.Exec("ROLLBACK TO SAVEPOINT incompatible_down")
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRow("SELECT count(*) FROM im_channel_sessions").Scan(&count))
	require.Equal(t, 7, count, "failed rollback must preserve every mapping")
	// With compatible data, down and up both work and preserve the old row.
	_, err = tx.Exec("DELETE FROM im_channel_sessions WHERE id != 'legacy'")
	require.NoError(t, err)
	require.NoError(t, run("000137_im_session_mode_indexes.down.sql"))
	require.NoError(t, run("000137_im_session_mode_indexes.up.sql"))
	require.NoError(t, tx.QueryRow("SELECT count(*) FROM im_channel_sessions WHERE id = 'legacy'").Scan(&count))
	require.Equal(t, 1, count)
}
