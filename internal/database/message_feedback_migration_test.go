package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationIncludesMessageChunkFeedbackSchema(t *testing.T) {
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	projectRoot := filepath.Clean(filepath.Join(originalDir, "..", ".."))
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalDir))
	})

	dbPath := filepath.Join(t.TempDir(), "feedback.db")
	require.NoError(t, RunMigrationsWithOptions(
		"sqlite3://"+dbPath,
		MigrationOptions{SQLiteDBPath: dbPath},
	))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	expectedTables := []string{
		"message_chunk_references",
		"message_feedbacks",
		"message_feedback_attributions",
		"chunk_feedback_weight_logs",
	}
	for _, table := range expectedTables {
		var count int
		require.NoError(t, db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count))
		require.Equal(t, 1, count, table)
	}

	rows, err := db.Query("PRAGMA table_info(chunks)")
	require.NoError(t, err)
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal interface{}
			primaryKey int
		)
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey))
		columns[name] = true
	}
	require.NoError(t, rows.Err())
	for _, column := range []string{
		"positive_feedback_count",
		"negative_feedback_count",
		"positive_feedback_rate",
		"recall_weight",
		"feedback_status",
	} {
		require.True(t, columns[column], column)
	}
}
