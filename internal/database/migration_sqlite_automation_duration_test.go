package database

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteAutomationDurationUpgradePreservesExistingRuns(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	for _, version := range []int{45, 47} {
		t.Run(fmt.Sprintf("from-v%d", version), func(t *testing.T) {
			chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, version))
			dbPath := filepath.Join(t.TempDir(), "automations.db")
			require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
			db := openSQLiteDB(t, dbPath)
			_, err := db.Exec(`INSERT INTO automation_runs
				(id, automation_id, tenant_id, scheduled_at, trigger_kind, status, answer, created_at, updated_at)
				VALUES ('existing-run', 'automation', 1, CURRENT_TIMESTAMP, 'schedule',
					'awaiting_approval', 'saved answer', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			require.NoError(t, err)

			chdirAndRestore(t, repoRoot)
			require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
			var duration int64
			var answer, status string
			require.NoError(t, db.QueryRow(`SELECT execution_duration_ms, answer, status
				FROM automation_runs WHERE id = 'existing-run'`).Scan(&duration, &answer, &status))
			require.Zero(t, duration)
			require.Equal(t, "saved answer", answer)
			require.Equal(t, "awaiting_approval", status)
		})
	}
}
