package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationsUpgradeV33PreservesMCPServices(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 33))
	dbPath := filepath.Join(t.TempDir(), "upgrade-mcp.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO mcp_services (id, tenant_id, name, description, transport_type)
		VALUES ('existing', 1, 'Existing service', 'Legacy documentation', 'sse')`)
	require.NoError(t, err)

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	// Starting again must not replay the column addition or change existing data.
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	var name, description, instructions string
	require.NoError(t, db.QueryRow(`SELECT name, description, usage_instructions
		FROM mcp_services WHERE id = 'existing'`).Scan(&name, &description, &instructions))
	require.Equal(t, "Existing service", name)
	require.Equal(t, "Legacy documentation", description)
	require.Empty(t, instructions)

	_, err = db.Exec(`INSERT INTO mcp_services (id, tenant_id, name, transport_type, usage_instructions)
		VALUES ('new', 1, 'New service', 'sse', 'Use for contracts')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE mcp_services SET usage_instructions = 'Updated instructions' WHERE id = 'new'`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT usage_instructions FROM mcp_services WHERE id = 'new'`).Scan(&instructions))
	require.Equal(t, "Updated instructions", instructions)

	// Directory refresh uses a tenant/service/principal upsert and JSON tool counts.
	const upsert = `INSERT INTO mcp_metadata
		(tenant_id, service_id, principal, config_fingerprint, tools, synced_at)
		VALUES (1, 'new', ?, 'fingerprint', ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tenant_id, service_id, principal) DO UPDATE SET tools = excluded.tools`
	_, err = db.Exec(upsert, "", `[]`)
	require.NoError(t, err)
	_, err = db.Exec(upsert, "", `[{"name":"contracts"}]`)
	require.NoError(t, err)
	_, err = db.Exec(upsert, "user-a", `[]`)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT json_array_length(tools) FROM mcp_metadata
		WHERE tenant_id = 1 AND service_id = 'new' AND principal = ''`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM mcp_metadata WHERE service_id = 'new'`).Scan(&count))
	require.Equal(t, 2, count)
}
