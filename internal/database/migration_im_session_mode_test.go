package database

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationsSeparateIMSessionModes(t *testing.T) {
	root := sqliteRepoRoot(t)
	for _, upgrade := range []bool{false, true} {
		name := "fresh"
		if upgrade {
			name = "upgrade"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "im.db")
			opts := MigrationOptions{SQLiteDBPath: path}
			if upgrade {
				chdirAndRestore(t, copySQLiteMigrationsThrough(t, root, 52))
				require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
			}
			db := openSQLiteDB(t, path)
			if upgrade {
				insertIMMapping(t, db, "legacy", "alice", "", "agent-1", false)
			}
			chdirAndRestore(t, root)
			require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
			if upgrade {
				var id string
				row := db.QueryRow("SELECT session_id FROM im_channel_sessions WHERE id = 'legacy'")
				require.NoError(t, row.Scan(&id))
				require.Equal(t, "session-legacy", id, "upgrade must preserve existing mappings")
			} else {
				insertIMMapping(t, db, "legacy", "alice", "", "agent-1", false)
			}
			assertIMMappingConstraints(t, db)
		})
	}
}

// The fixture exercises the partial indexes independently of the session
// resolver. These fields have no foreign keys other than session_id on the
// real schema, so seed the backing session before inserting each mapping.
func insertIMMapping(t *testing.T, db *sql.DB, id, user, thread, agent string, deleted bool) {
	t.Helper()
	_, err := db.Exec("INSERT INTO sessions (id, tenant_id) VALUES (?, 1)", "session-"+id)
	require.NoError(t, err)
	var deletedAt any
	if deleted {
		deletedAt = "2026-01-01 00:00:00"
	}
	_, err = db.Exec(`INSERT INTO im_channel_sessions
		(id, platform, user_id, chat_id, session_id, tenant_id, agent_id, thread_id, deleted_at)
		VALUES (?, 'feishu', ?, 'group', ?, 1, ?, ?, ?)`, id, user, "session-"+id, agent, thread, deletedAt)
	require.NoError(t, err)
}

func assertIMMappingConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	insertIMMapping(t, db, "topic-1", "alice", "thread-1", "agent-1", false)
	insertIMMapping(t, db, "topic-2", "alice", "thread-2", "agent-1", false)
	insertIMMapping(t, db, "agent-user", "alice", "", "agent-2", false)
	insertIMMapping(t, db, "agent-thread", "alice", "thread-1", "agent-2", false)
	insertIMMapping(t, db, "deleted-user", "alice", "", "agent-1", true)
	insertIMMapping(t, db, "deleted-thread", "alice", "thread-1", "agent-1", true)
	// Duplicates within one mode are still rejected, including a different
	// creator trying to insert another mapping for an existing shared thread.
	for _, tc := range []struct{ user, thread string }{{"alice", ""}, {"bob", "thread-1"}} {
		_, err := db.Exec(`INSERT INTO im_channel_sessions
			(id, platform, user_id, chat_id, session_id, tenant_id, agent_id, thread_id)
			VALUES ('duplicate', 'feishu', ?, 'group', 'session-legacy', 1, 'agent-1', ?)`, tc.user, tc.thread)
		require.ErrorContains(t, err, "UNIQUE constraint failed")
	}
}

func TestSQLiteIMSessionModeRollbackPreservesData(t *testing.T) {
	root := sqliteRepoRoot(t)
	for _, conflicting := range []bool{false, true} {
		name := "compatible"
		if conflicting {
			name = "multiple threads"
		}
		t.Run(name, func(t *testing.T) {
			chdirAndRestore(t, root)
			path := filepath.Join(t.TempDir(), "rollback.db")
			require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: path}))
			db := openSQLiteDB(t, path)
			insertIMMapping(t, db, "legacy", "alice", "", "agent-1", false)
			if conflicting {
				insertIMMapping(t, db, "thread", "alice", "topic", "agent-1", false)
			}
			down, err := os.ReadFile(filepath.Join(root, "migrations/sqlite/000053_im_session_mode_indexes.down.sql"))
			require.NoError(t, err)
			driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
			require.NoError(t, err)
			err = driver.Run(bytes.NewReader(down))
			if conflicting {
				require.ErrorContains(t, err, "UNIQUE constraint failed")
				// A failed down must leave the new indexes and all rows intact.
				insertIMMapping(t, db, "another-thread", "alice", "other-topic", "agent-1", false)
				var live int
				row := db.QueryRow("SELECT count(*) FROM im_channel_sessions WHERE deleted_at IS NULL")
				require.NoError(t, row.Scan(&live))
				require.Equal(t, 3, live)
			} else {
				require.NoError(t, err)
				assertIMMappingConstraintsAfterReupgrade(t, db, root)
			}
		})
	}
}

func assertIMMappingConstraintsAfterReupgrade(t *testing.T, db *sql.DB, root string) {
	t.Helper()
	up, err := os.ReadFile(filepath.Join(root, "migrations/sqlite/000053_im_session_mode_indexes.up.sql"))
	require.NoError(t, err)
	driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
	require.NoError(t, err)
	require.NoError(t, driver.Run(bytes.NewReader(up)))
	assertIMMappingConstraints(t, db)
}
