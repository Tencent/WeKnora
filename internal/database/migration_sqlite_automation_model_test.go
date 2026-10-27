package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteAutomationModelUpgradePreservesDefault(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 48))
	dbPath := filepath.Join(t.TempDir(), "automations.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO automations
		(id, tenant_id, owner_user_id, name, agent_id, instruction, trigger_config, limits, created_at, updated_at)
		VALUES ('task', 1, 'owner', 'Reminder', 'agent', 'Take a break', '{}', '{}',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	var model, name string
	require.NoError(t, db.QueryRow("SELECT model_id, name FROM automations WHERE id = 'task'").Scan(&model, &name))
	require.Empty(t, model, "automations must use the assistant default")
	require.Equal(t, "Reminder", name)
	_, err = db.Exec("UPDATE automations SET model_id = 'chosen-model' WHERE id = 'task'")
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT model_id FROM automations WHERE id = 'task'").Scan(&model))
	require.Equal(t, "chosen-model", model)
}
