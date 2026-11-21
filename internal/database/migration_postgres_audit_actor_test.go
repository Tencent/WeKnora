package database

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPostgresAuditLogActorUserIDFitsSyntheticPrincipals pins the column width
// widened by 000135. Embed channels and MCP endpoints authenticate without a
// users row and mint "embed-<channel uuid>" (42 chars) / "mcp-<endpoint uuid>"
// (40 chars) actor ids; under VARCHAR(36) every audit insert they triggered
// failed with SQLSTATE 22001 and was dropped silently (issue #4829). SQLite
// cannot catch this — it does not enforce VARCHAR lengths — so the guard is
// PostgreSQL-only.
func TestPostgresAuditLogActorUserIDFitsSyntheticPrincipals(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	require.NoError(t, RunMigrationsWithOptions(dsn, MigrationOptions{}))

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var width sql.NullInt64
	require.NoError(t, db.QueryRow(
		`SELECT character_maximum_length FROM information_schema.columns
		 WHERE table_name = 'audit_logs' AND column_name = 'actor_user_id'`).Scan(&width))
	require.True(t, width.Valid, "audit_logs.actor_user_id must exist after migration 000044")
	require.GreaterOrEqual(t, width.Int64, int64(42), "embed-<channel uuid> actor ids are 42 characters")

	for _, actorID := range []string{
		"embed-4f659efc-8e27-4dee-b232-b053c369e44f", // embed_auth.go, 42 chars
		"mcp-37aa4256-9727-4228-8d1f-bd5ebe5cf433",   // mcp_endpoint_auth.go, 40 chars
	} {
		_, err := db.Exec(
			`INSERT INTO audit_logs (tenant_id, actor_user_id, action) VALUES (1, $1, 'test.actor_width')`,
			actorID,
		)
		require.NoErrorf(t, err, "synthetic actor id %q must fit audit_logs.actor_user_id", actorID)
	}

	_, err = db.Exec(`DELETE FROM audit_logs WHERE action = 'test.actor_width'`)
	require.NoError(t, err)
}
