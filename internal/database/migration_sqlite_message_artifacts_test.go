package database

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSQLiteMessageArtifactsBackfillAndRollback upgrades a v22 database that
// still keeps artifacts on messages.artifacts, checks 000023 copies them into
// message_artifacts, then replays the down migration and checks the JSON
// column is rebuilt from the table in a shape Go can decode.
func TestSQLiteMessageArtifactsBackfillAndRollback(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	legacyRoot := copySQLiteMigrationsThrough(t, repoRoot, 22)
	chdirAndRestore(t, legacyRoot)
	dbPath := filepath.Join(t.TempDir(), "artifacts.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	legacy := `[
		{"url":"local://a/report.pptx","file_name":"report.pptx","file_type":".pptx","file_size":2048,
		 "content_hash":"abc","source_path":"/workspace/report.pptx",
		 "mod_time":"2026-09-01T10:00:00.123456789+08:00","created_at":"2026-09-01T10:00:05+08:00"},
		{"url":"local://a/chart.png","file_name":"chart.png","file_type":".png","file_size":10,
		 "source_path":"/workspace/chart.png","mod_time":"2026-09-01T10:00:00Z","created_at":"2026-09-01T02:00:06Z"}
	]`
	_, err := db.Exec(
		`INSERT INTO messages (id, request_id, session_id, role, content, artifacts, created_at)
		 VALUES ('msg-1', 'req-1', 'sess-1', 'assistant', 'done', ?, '2026-09-01 02:00:00')`,
		legacy,
	)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO messages (id, request_id, session_id, role, content, artifacts)
		 VALUES ('msg-2', 'req-2', 'sess-1', 'user', 'hi', '[]')`,
	)
	require.NoError(t, err)
	// Malformed values must fall back instead of failing the migration.
	_, err = db.Exec(
		`INSERT INTO messages (id, request_id, session_id, role, content, artifacts, created_at)
		 VALUES ('msg-3', 'req-3', 'sess-1', 'assistant', 'dirty', ?, '2026-09-02 00:00:00')`,
		`[{"file_name":"dirty.bin","file_size":"12.5x","created_at":"garbage"}, 7, "x"]`,
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db = openSQLiteDB(t, dbPath)

	type row struct {
		position   int
		fileName   string
		fileSize   int64
		sourcePath string
		modTime    string
		createdAt  time.Time
	}
	rows, err := db.Query(
		`SELECT position, file_name, file_size, source_path, mod_time, created_at
		 FROM message_artifacts WHERE message_id = 'msg-1' ORDER BY position`,
	)
	require.NoError(t, err)
	var got []row
	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.position, &r.fileName, &r.fileSize, &r.sourcePath, &r.modTime, &r.createdAt))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	require.Len(t, got, 2)
	require.Equal(t, 0, got[0].position)
	require.Equal(t, "report.pptx", got[0].fileName)
	require.Equal(t, int64(2048), got[0].fileSize)
	require.Equal(t, "/workspace/report.pptx", got[0].sourcePath)
	require.Equal(t, "2026-09-01T10:00:00.123456789+08:00", got[0].modTime, "mod_time keeps the exact RFC 3339 text")
	wantCreated, _ := time.Parse(time.RFC3339, "2026-09-01T02:00:05Z")
	require.True(t, got[0].createdAt.Equal(wantCreated), "created_at normalised to UTC: %v", got[0].createdAt)
	require.Equal(t, 1, got[1].position)
	require.Equal(t, "chart.png", got[1].fileName)

	var dirtySize int64
	var dirtyCreated time.Time
	require.NoError(t, db.QueryRow(
		`SELECT file_size, created_at FROM message_artifacts WHERE message_id = 'msg-3'`,
	).Scan(&dirtySize, &dirtyCreated))
	require.EqualValues(t, 12, dirtySize, "SQLite CAST keeps the numeric prefix")
	require.True(t, dirtyCreated.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)),
		"an unparseable created_at falls back to the message time: %v", dirtyCreated)

	var userRows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM message_artifacts WHERE message_id = 'msg-2'`).Scan(&userRows))
	require.Zero(t, userRows)

	// The legacy column is cleared once copied, so history loads (SELECT *)
	// no longer read a stale duplicate; rows without artifacts are untouched.
	var legacy1, legacy2 sql.NullString
	require.NoError(t, db.QueryRow(`SELECT artifacts FROM messages WHERE id = 'msg-1'`).Scan(&legacy1))
	require.NoError(t, db.QueryRow(`SELECT artifacts FROM messages WHERE id = 'msg-2'`).Scan(&legacy2))
	require.False(t, legacy1.Valid, "copied artifacts are cleared from messages.artifacts")
	require.Equal(t, "[]", legacy2.String)

	// Rollback rebuilds the legacy column from the table.
	down, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "sqlite", "000023_message_artifacts_table.down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	require.False(t, sqliteTableExists(t, db, "message_artifacts"))

	var restored string
	require.NoError(t, db.QueryRow(`SELECT artifacts FROM messages WHERE id = 'msg-1'`).Scan(&restored))
	var decoded []struct {
		FileName  string    `json:"file_name"`
		FileSize  int64     `json:"file_size"`
		ModTime   time.Time `json:"mod_time"`
		CreatedAt time.Time `json:"created_at"`
	}
	wantMod, _ := time.Parse(time.RFC3339Nano, "2026-09-01T10:00:00.123456789+08:00")
	require.NoError(t, json.Unmarshal([]byte(restored), &decoded))
	require.Len(t, decoded, 2)
	require.Equal(t, "report.pptx", decoded[0].FileName)
	require.Equal(t, int64(2048), decoded[0].FileSize)
	require.True(t, decoded[0].CreatedAt.Equal(wantCreated))
	require.True(t, decoded[0].ModTime.Equal(wantMod), "rollback keeps nanosecond mtime")
	require.Equal(t, "chart.png", decoded[1].FileName)
}
