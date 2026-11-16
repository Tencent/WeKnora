package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteAutomationAssistantRemovesExecutorSelection(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 49))
	dbPath := filepath.Join(t.TempDir(), "automations.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO automations
		(id, tenant_id, owner_user_id, name, agent_id, model_id, instruction, trigger_config, created_at, updated_at)
		VALUES ('task', 1, 'owner', 'Digest', 'custom-agent', 'chosen-model', 'Summarize the news', '{}',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('automations')
		WHERE name = 'agent_id'`).Scan(&count))
	require.Zero(t, count)
	var model, instruction string
	require.NoError(t, db.QueryRow(`SELECT model_id, instruction FROM automations
		WHERE id = 'task'`).Scan(&model, &instruction))
	require.Equal(t, "chosen-model", model)
	require.Equal(t, "Summarize the news", instruction)
	_, err = db.Exec(`INSERT INTO automations
		(id, tenant_id, owner_user_id, name, instruction, trigger_config, created_at, updated_at)
		VALUES ('new-task', 1, 'owner', 'Reminder', 'Take a break', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err, "new automations must not need an executor field")
}
