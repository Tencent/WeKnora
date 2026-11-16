package database

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the migration SQL itself with legacy API-created settings, and
// preserve credentials, unrelated settings, explicit markers and other sources.
func testFeishuResyncMigration(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	tx, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	jsonType := "TEXT"
	migration := "sqlite/000040_feishu_deep_adaptation_resync"
	if dialect == "postgres" {
		jsonType = "JSONB"
		migration = "versioned/000122_feishu_deep_adaptation_resync"
	}
	_, err = tx.Exec("CREATE TEMP TABLE data_sources " +
		"(id TEXT PRIMARY KEY, type TEXT, config " + jsonType + ", deleted_at TEXT)")
	require.NoError(t, err)
	cases := []struct {
		id, kind, config string
		deleted          bool
		wantMarker       bool
	}{
		{"missing", "feishu", `{"credentials":{"app_id":"a","app_secret":"s"},"resource_ids":["space"]}`, false, true},
		{"null", "lark", `{"settings":null,"credentials":{"app_secret":"s"}}`, false, true},
		{"empty", "feishu_drive", `{"settings":{}}`, false, true},
		{"configured", "lark_drive", `{"settings":{"parse_mode":"export","timezone":"Asia/Shanghai"}}`, false, true},
		{"false-marker", "feishu", `{"settings":{"resync_required":false,"parse_mode":"blocks"}}`, false, false},
		{"true-marker", "lark", `{"settings":{"resync_required":true,"timezone":"UTC"}}`, false, true},
		{"other", "gitlab", `{"settings":null}`, false, false},
		{"deleted", "feishu", `{"settings":null}`, true, false},
	}
	for _, tt := range cases {
		var deleted any
		if tt.deleted {
			deleted = "2026-01-01"
		}
		_, err = tx.Exec("INSERT INTO data_sources (id,type,config,deleted_at) VALUES ($1,$2,$3,$4)",
			tt.id, tt.kind, tt.config, deleted)
		require.NoError(t, err)
	}
	script, err := os.ReadFile(filepath.Join(sqliteRepoRoot(t), "migrations", migration+".up.sql"))
	require.NoError(t, err)
	for range 2 {
		_, err = tx.Exec(string(script))
		require.NoError(t, err)
	}
	for _, tt := range cases {
		var raw string
		require.NoError(t, tx.QueryRow("SELECT config FROM data_sources WHERE id=$1", tt.id).Scan(&raw))
		var got, before map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &got))
		require.NoError(t, json.Unmarshal([]byte(tt.config), &before))
		if !tt.wantMarker {
			require.Equal(t, before, got, tt.id)
			continue
		}
		settings, ok := got["settings"].(map[string]any)
		require.True(t, ok, tt.id)
		require.Equal(t, true, settings["resync_required"], tt.id)
		require.Equal(t, before["credentials"], got["credentials"], tt.id)
		require.Equal(t, before["resource_ids"], got["resource_ids"], tt.id)
		if old, ok := before["settings"].(map[string]any); ok {
			for key, value := range old {
				require.Equal(t, value, settings[key], tt.id+":"+key)
			}
		}
	}
	down, err := os.ReadFile(filepath.Join(sqliteRepoRoot(t), "migrations", migration+".down.sql"))
	require.NoError(t, err)
	_, err = tx.Exec(string(down))
	require.NoError(t, err)
	var raw string
	require.NoError(t, tx.QueryRow("SELECT config FROM data_sources WHERE id='configured'").Scan(&raw))
	require.JSONEq(t, `{"settings":{"parse_mode":"export","timezone":"Asia/Shanghai"}}`, raw)
}

func TestSQLiteFeishuResyncMigration(t *testing.T) {
	db := openSQLiteDB(t, filepath.Join(t.TempDir(), "resync.db"))
	testFeishuResyncMigration(t, db, "sqlite")
}

func TestPostgresFeishuResyncMigration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	testFeishuResyncMigration(t, db, "postgres")
}
