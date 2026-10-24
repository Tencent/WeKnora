package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

func TestSQLiteIMReplyLanguageUpgradeAndRollback(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 41))
	dbPath := filepath.Join(t.TempDir(), "im-reply-language.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO sessions (id, tenant_id) VALUES ('session', 1)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO im_channels (id, tenant_id, agent_id, platform, name, locale)
		VALUES ('channel', 1, 'agent', 'feishu', 'Existing channel', 'zh-CN')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO im_channel_sessions (id, platform, user_id, session_id, tenant_id, im_channel_id)
		VALUES ('mapping', 'feishu', 'user', 'session', 1, 'channel')`)
	require.NoError(t, err)

	upgradeRoot := copySQLiteMigrationsThrough(t, repoRoot, 51)
	downFile := filepath.Join("migrations", "sqlite", "000051_im_follow_user_language.down.sql")
	downSQL, err := os.ReadFile(filepath.Join(repoRoot, downFile))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(upgradeRoot, downFile), downSQL, 0o600))
	chdirAndRestore(t, upgradeRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	// Repeated startup must not replay the ALTER TABLE statements.
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, 51, version)
	require.False(t, dirty)
	var name, locale, mode, language, sessionID string
	require.NoError(t, db.QueryRow(`SELECT name, locale, language_mode FROM im_channels
		WHERE id = 'channel'`).Scan(&name, &locale, &mode))
	require.Equal(t, "Existing channel", name)
	require.Equal(t, "zh-CN", locale)
	require.Equal(t, "fixed", mode)
	require.NoError(t, db.QueryRow(`SELECT session_id, last_detected_language FROM im_channel_sessions
		WHERE id = 'mapping'`).Scan(&sessionID, &language))
	require.Equal(t, "session", sessionID)
	require.Empty(t, language)
	_, err = db.Exec(`UPDATE im_channels SET language_mode = 'follow_user' WHERE id = 'channel'`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT language_mode FROM im_channels
		WHERE id = 'channel'`).Scan(&mode))
	require.Equal(t, "follow_user", mode)
	_, err = db.Exec(`UPDATE im_channel_sessions SET last_detected_language = 'vi' WHERE id = 'mapping'`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT last_detected_language FROM im_channel_sessions
		WHERE id = 'mapping'`).Scan(&language))
	require.Equal(t, "vi", language)

	driver, err := sqlite3migrate.WithInstance(db, &sqlite3migrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))
	version, dirty = sqliteMigrationState(t, db)
	require.Equal(t, 50, version)
	require.False(t, dirty)
	require.False(t, sqliteColumnExists(t, db, "im_channels", "language_mode"))
	require.False(t, sqliteColumnExists(t, db, "im_channel_sessions", "last_detected_language"))
	require.NoError(t, db.QueryRow(`SELECT name, locale FROM im_channels
		WHERE id = 'channel'`).Scan(&name, &locale))
	require.Equal(t, "Existing channel", name)
	require.Equal(t, "zh-CN", locale)
	require.NoError(t, db.QueryRow(`SELECT session_id FROM im_channel_sessions
		WHERE id = 'mapping'`).Scan(&sessionID))
	require.Equal(t, "session", sessionID)
	require.NoError(t, m.Steps(1))
	version, dirty = sqliteMigrationState(t, db)
	require.Equal(t, 51, version)
	require.False(t, dirty)
	require.NoError(t, db.QueryRow(`SELECT language_mode FROM im_channels
		WHERE id = 'channel'`).Scan(&mode))
	require.Equal(t, "fixed", mode)
	require.NoError(t, db.QueryRow(`SELECT last_detected_language FROM im_channel_sessions
		WHERE id = 'mapping'`).Scan(&language))
	require.Empty(t, language)
}
