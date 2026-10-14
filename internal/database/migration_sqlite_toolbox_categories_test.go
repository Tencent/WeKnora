package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationsUpgradeV34AddsToolboxCategories(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 34))
	dbPath := filepath.Join(t.TempDir(), "upgrade-toolbox.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	require.False(t, sqliteTableExists(t, db, "toolbox_categories"))
	_, err := db.Exec(`INSERT INTO mcp_services (id, tenant_id, name, transport_type, usage_instructions)
		VALUES ('existing', 1, 'Existing service', 'sse', 'Keep these instructions')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO mcp_metadata
		(tenant_id, service_id, principal, config_fingerprint, tools, synced_at)
		VALUES (1, 'existing', '', 'fingerprint', '[{"name":"contracts"}]', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, version)
	require.False(t, dirty)
	for _, table := range []string{"toolbox_categories", "toolbox_category_skills", "toolbox_category_mcp_services"} {
		require.True(t, sqliteTableExists(t, db, table), table)
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		require.Zero(t, count, "existing resources start without tags")
	}
	_, err = db.Exec(`INSERT INTO toolbox_categories (id, tenant_id, name) VALUES ('contracts', 1, 'Contracts')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO toolbox_category_mcp_services (category_id, mcp_service_id)
		VALUES ('contracts', 'existing')`)
	require.NoError(t, err)

	// Repeated startup preserves both the new association and the earlier MCP metadata.
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	var name, instructions, tools string
	require.NoError(t, db.QueryRow(`SELECT c.name, s.usage_instructions, m.tools
		FROM toolbox_categories c
		JOIN toolbox_category_mcp_services r ON r.category_id = c.id
		JOIN mcp_services s ON s.id = r.mcp_service_id
		JOIN mcp_metadata m ON m.service_id = s.id AND m.tenant_id = c.tenant_id
		WHERE c.id = 'contracts'`).Scan(&name, &instructions, &tools))
	require.Equal(t, "Contracts", name)
	require.Equal(t, "Keep these instructions", instructions)
	require.JSONEq(t, `[{"name":"contracts"}]`, tools)
}
