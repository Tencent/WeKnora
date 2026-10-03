package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

// TestPostgresMigrationsServeAgentHistory runs the versioned migrations against
// a real PostgreSQL. Skipped unless WEKNORA_MIGRATION_TEST_POSTGRES_DSN points
// at a disposable database, for example:
//
//	docker run -d --rm -e POSTGRES_PASSWORD=pg -e POSTGRES_DB=weknora -p 55432:5432 \
//	  paradedb/paradedb:v0.22.6-pg17
//	WEKNORA_MIGRATION_TEST_POSTGRES_DSN=postgres://postgres:pg@localhost:55432/weknora?sslmode=disable
//
// It checks what SQLite cannot: that 000106 builds its index CONCURRENTLY
// through golang-migrate (which fails inside a transaction block), in both
// directions, and that the agent history queries walk it without sorting.
func TestPostgresMigrationsServeAgentHistory(t *testing.T) {
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

	var version int
	var dirty bool
	require.NoError(t, db.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty))
	require.Equal(t, latestVersionedMigration(t, root), version)
	require.False(t, dirty)
	requirePostgresIndexValid(t, db)

	// Down and up again: DROP/CREATE INDEX CONCURRENTLY through golang-migrate.
	m, err := migrate.New("file://migrations/versioned", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))
	var indexes int
	require.NoError(t, db.QueryRow(
		"SELECT count(*) FROM pg_class WHERE relname = 'idx_messages_session_created_id'").Scan(&indexes))
	require.Zero(t, indexes, "the down migration drops the index")
	require.NoError(t, m.Steps(1))
	requirePostgresIndexValid(t, db)

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// An empty table is always cheapest to scan; rule the scan out to see
	// which index the planner can use and whether it still has to sort.
	_, err = conn.ExecContext(ctx, "SET enable_seqscan = off")
	require.NoError(t, err)
	for name, query := range map[string]string{
		"backwards page": `SELECT * FROM messages WHERE session_id = 's'
			AND (created_at < now() OR (created_at = now() AND id < 'x'))
			AND deleted_at IS NULL ORDER BY created_at DESC, id DESC LIMIT 200`,
		"newest checkpoint": `SELECT id FROM messages WHERE session_id = 's' AND role = 'assistant'
			AND context_checkpoint IS NOT NULL AND deleted_at IS NULL
			ORDER BY created_at DESC, id DESC LIMIT 1`,
	} {
		rows, err := conn.QueryContext(ctx, "EXPLAIN "+query)
		require.NoError(t, err, name)
		var plan strings.Builder
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line), name)
			plan.WriteString(line + "\n")
		}
		require.NoError(t, rows.Close(), name)
		require.Contains(t, plan.String(), "idx_messages_session_created_id", "%s plan:\n%s", name, plan.String())
		require.NotContains(t, plan.String(), "Sort", "%s must not sort:\n%s", name, plan.String())
	}
}

func requirePostgresIndexValid(t *testing.T, db *sql.DB) {
	t.Helper()
	var valid bool
	require.NoError(t, db.QueryRow(`SELECT i.indisvalid FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'idx_messages_session_created_id'`).Scan(&valid))
	require.True(t, valid, "a CONCURRENTLY build that failed leaves an INVALID index")
}

func latestVersionedMigration(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "migrations", "versioned"))
	require.NoError(t, err)
	latest := 0
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(prefix); err == nil && n > latest {
			latest = n
		}
	}
	return latest
}
