package database

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPostgresAgentPromptAcceptsEmbedPrincipal pins the width widened by
// 000138. Embed sessions identify the prompt owner as
// "embed_session:<tenant>:<channel>:<session>" (93 characters for current
// UUIDs), which overflowed VARCHAR(64). Creating the prompt failed with
// SQLSTATE 22001, so embed visitors never received an in-conversation MCP
// OAuth card. The same id is written to resolved_by when the visitor answers.
//
// SQLite cannot catch this because it does not enforce VARCHAR lengths, so
// the guard is PostgreSQL-only.
func TestPostgresAgentPromptAcceptsEmbedPrincipal(t *testing.T) {
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

	for _, column := range []string{"user_id", "resolved_by"} {
		var width sql.NullInt64
		require.NoError(t, db.QueryRow(
			`SELECT character_maximum_length FROM information_schema.columns
			 WHERE table_name = 'agent_prompts' AND column_name = $1`, column).Scan(&width))
		require.Truef(t, width.Valid, "agent_prompts.%s must exist after migration 000119", column)
		require.GreaterOrEqualf(t, width.Int64, int64(93),
			"agent_prompts.%s must fit an embed_session principal", column)
	}

	const (
		promptID  = "5c4f4d6e-2b2a-4a8e-8f8a-1f4f3f0f4e01"
		sessionID = "5c4f4d6e-2b2a-4a8e-8f8a-1f4f3f0f4e02"
		messageID = "5c4f4d6e-2b2a-4a8e-8f8a-1f4f3f0f4e03"
		principal = "embed_session:10000:b60edf5c-d7da-4a58-95c8-4d020d3099ef:22777ab5-0477-4cbd-95e8-d5b17f414396"
	)
	require.Len(t, principal, 93)

	_, err = db.Exec(
		`INSERT INTO agent_prompts
		 (id, tenant_id, user_id, session_id, assistant_message_id, kind, status,
		  resolved_by, owner_instance)
		 VALUES ($1, 1, $2, $3, $4, 'mcp_oauth', 'pending', $2, 'owner')`,
		promptID, principal, sessionID, messageID,
	)
	require.NoError(t, err, "an embed-session principal must fit agent_prompts")

	var storedUser, storedResolver string
	require.NoError(t, db.QueryRow(
		`SELECT user_id, resolved_by FROM agent_prompts WHERE id = $1`, promptID,
	).Scan(&storedUser, &storedResolver))
	require.Equal(t, principal, storedUser)
	require.Equal(t, principal, storedResolver)

	_, err = db.Exec(`DELETE FROM agent_prompts WHERE id = $1`, promptID)
	require.NoError(t, err)
}
