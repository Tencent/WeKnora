package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteMCPToolSessionPolicyMigration(t *testing.T) {
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, root, 53))
	dbPath := filepath.Join(t.TempDir(), "session-policies.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	_, err := db.Exec(`INSERT INTO mcp_services (id, tenant_id, name, transport_type)
 VALUES ('svc', 7, 'Tickets', 'sse')`)
	require.NoError(t, err)
	chdirAndRestore(t, root)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	_, err = db.Exec(`INSERT INTO mcp_tool_session_policies (tenant_id, service_id, tool_name, allowed_sessions)
		VALUES (7, 'svc', 'tool', '[]')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO mcp_tool_session_policies (tenant_id, service_id, tool_name, allowed_sessions)
		VALUES (7, 'svc', 'tool', '[]')`)
	require.Error(t, err, "one policy per tenant, service and tool")
	_, err = db.Exec(`INSERT INTO mcp_tool_session_policies (tenant_id, service_id, tool_name, allowed_sessions)
		VALUES (8, 'svc', 'tool', '[]')`)
	require.NoError(t, err, "tenant is part of the key")
}
