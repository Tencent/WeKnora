// Package database implements database initialization and migrations.
package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDingtalkStreamMigrationPreservesOtherChannels(t *testing.T) {
	for _, migration := range []string{
		"sqlite/000017_dingtalk_stream_only.up.sql",
		"versioned/000096_dingtalk_stream_only.up.sql",
	} {
		t.Run(migration, func(t *testing.T) {
			db := openSQLiteDB(t, filepath.Join(t.TempDir(), "channels.db"))
			_, err := db.Exec("CREATE TABLE im_channels (id TEXT, platform TEXT, mode TEXT, updated_at TIMESTAMP)")
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO im_channels VALUES " +
				"('d', 'dingtalk', 'webhook', NULL), " +
				"('s', 'slack', 'webhook', NULL), " +
				"('w', 'dingtalk', 'websocket', NULL)")
			require.NoError(t, err)
			script, err := os.ReadFile(filepath.Join(sqliteRepoRoot(t), "migrations", migration))
			require.NoError(t, err)
			_, err = db.Exec(string(script))
			require.NoError(t, err)
			for id, expected := range map[string]string{"d": "websocket", "s": "webhook", "w": "websocket"} {
				var mode string
				require.NoError(t, db.QueryRow("SELECT mode FROM im_channels WHERE id = ?", id).Scan(&mode))
				require.Equal(t, expected, mode)
			}
		})
	}
}
