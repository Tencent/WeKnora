package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// MCP services plugins provide are not rows of mcp_services; their tool
// policies must still be storable with foreign keys enforced.
func TestSQLiteMCPToolApprovalsAcceptPluginServices(t *testing.T) {
	chdirAndRestore(t, sqliteRepoRoot(t))
	dbPath := filepath.Join(t.TempDir(), "migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO mcp_tool_approvals (id, tenant_id, service_id, tool_name, enabled)
		VALUES ('a', 1, '6f1d0f4c-0000-5000-8000-000000000001', 'search', 0)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO mcp_tool_approvals (id, tenant_id, service_id, tool_name)
		VALUES ('b', 1, '6f1d0f4c-0000-5000-8000-000000000001', 'search')`)
	require.Error(t, err, "the unique index on tenant, service and tool must survive the rebuild")
}
