package database

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const (
	wikiSourceAttemptFenceMigration = "000139_wiki_source_attempt_fence"
	wikiSourceAttemptFenceTrigger   = "trg_wiki_source_attempt_fence"
	wikiSourceAttemptFenceFunction  = "wiki_source_attempt_fence"
)

// TestPostgresWikiSourceAttemptFenceMigrationSQLRoundTrip uses the real spans
// migration in a private schema. Lock-contention tests belong to the repository
// suite; this test verifies the DDL lifecycle, schema binding and retained data.
func TestPostgresWikiSourceAttemptFenceMigrationSQLRoundTrip(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		t.Skip("set an isolated WEKNORA_MIGRATION_TEST_POSTGRES_DSN or WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	// Keep this schema-only search_path on one connection throughout the test.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	schema := "wiki_attempt_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := db.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, dropErr)
	})
	_, err = db.Exec("SET search_path TO " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	root := sqliteRepoRoot(t)
	_, err = db.Exec(readWikiProgressMigrationSQL(
		t, root, "versioned", "000055_knowledge_processing_spans.up.sql",
	))
	require.NoError(t, err)

	var retainedID int64
	require.NoError(t, db.QueryRow(`INSERT INTO knowledge_processing_spans
		(knowledge_id, attempt, span_id, name, kind, status, input, metadata)
		VALUES ('fence-source', 3, 'root', 'parse', 'root', 'running',
			'{"preserved":"input"}', '{"marker":"source-attempt-migration"}')
		RETURNING id`).Scan(&retainedID))
	readSentinel := func() string {
		t.Helper()
		var snapshot string
		require.NoError(t, db.QueryRow(
			"SELECT to_jsonb(s)::text FROM knowledge_processing_spans AS s WHERE id = $1",
			retainedID,
		).Scan(&snapshot))
		return snapshot
	}
	before := readSentinel()
	assertRetained := func() {
		t.Helper()
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM knowledge_processing_spans").Scan(&count))
		require.Equal(t, 1, count)
		require.Equal(t, before, readSentinel(), "trigger DDL must preserve the complete existing span row")
	}
	assertFence := func(enabled bool) {
		t.Helper()
		var triggerCount, functionCount int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pg_trigger
			WHERE tgrelid = 'knowledge_processing_spans'::regclass
				AND tgname = $1 AND NOT tgisinternal`,
			wikiSourceAttemptFenceTrigger,
		).Scan(&triggerCount))
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pg_proc AS p
			JOIN pg_namespace AS n ON n.oid = p.pronamespace
			WHERE n.nspname = $1 AND p.proname = $2 AND p.pronargs = 0`,
			schema, wikiSourceAttemptFenceFunction,
		).Scan(&functionCount))
		if !enabled {
			require.Zero(t, triggerCount)
			require.Zero(t, functionCount)
			return
		}
		require.Equal(t, 1, triggerCount)
		require.Equal(t, 1, functionCount)
		var triggerMode, functionSchema, functionName string
		require.NoError(t, db.QueryRow(`SELECT t.tgenabled::text, n.nspname, p.proname
			FROM pg_trigger AS t
			JOIN pg_proc AS p ON p.oid = t.tgfoid
			JOIN pg_namespace AS n ON n.oid = p.pronamespace
			WHERE t.tgrelid = 'knowledge_processing_spans'::regclass
				AND t.tgname = $1 AND NOT t.tgisinternal`,
			wikiSourceAttemptFenceTrigger,
		).Scan(&triggerMode, &functionSchema, &functionName))
		require.Equal(t, "A", triggerMode, "the source-attempt fence must be ENABLE ALWAYS")
		require.Equal(t, schema, functionSchema, "the trigger must use the function from this migration schema")
		require.Equal(t, wikiSourceAttemptFenceFunction, functionName)
	}

	up := readWikiProgressMigrationSQL(t, root, "versioned", wikiSourceAttemptFenceMigration+".up.sql")
	down := readWikiProgressMigrationSQL(t, root, "versioned", wikiSourceAttemptFenceMigration+".down.sql")
	assertFence(false)
	_, err = db.Exec(up)
	require.NoError(t, err)
	assertFence(true)
	assertRetained()

	_, err = db.Exec(down)
	require.NoError(t, err)
	assertFence(false)
	assertRetained()

	_, err = db.Exec(up)
	require.NoError(t, err)
	assertFence(true)
	assertRetained()
}
