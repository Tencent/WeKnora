package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

// TestPostgresMigrationsKeepAgentToolsWhenAnEmptyListMeansNone runs the
// versioned migrations against a real PostgreSQL; see
// TestPostgresMigrationsServeAgentHistory for how to start one. It stops
// before 000116, stores every kind of allowed_tools value, and checks that
// the upgrade keeps each agent's effective tools.
func TestPostgresMigrationsKeepAgentToolsWhenAnEmptyListMeansNone(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	m, err := migrate.New("file://migrations/versioned", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, ignoreNoChange(m.Migrate(versionedMigrationBefore(t, root, 116))))

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	const tenantID = 990116
	cleanup := func() { _, _ = db.Exec(`DELETE FROM custom_agents WHERE tenant_id = $1`, tenantID) }
	cleanup()
	t.Cleanup(cleanup)
	for id, config := range agentToolListRows {
		_, err := db.Exec(`INSERT INTO custom_agents (id, name, tenant_id, config) VALUES ($1, $1, $2, $3::jsonb)`,
			id, tenantID, config)
		require.NoError(t, err)
	}

	require.NoError(t, RunMigrationsWithOptions(dsn, MigrationOptions{}))
	requireAgentToolListsUpgraded(t, db,
		`SELECT config::text FROM custom_agents WHERE tenant_id = 990116 AND id = $1`)
}

// versionedMigrationBefore returns the highest versioned migration below
// version, so the test does not depend on how many migrations precede it.
func versionedMigrationBefore(t *testing.T, root string, version int) uint {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "migrations", "versioned"))
	require.NoError(t, err)
	before := 0
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(prefix); err == nil && n < version && n > before {
			before = n
		}
	}
	require.NotZero(t, before)
	return uint(before)
}

func ignoreNoChange(err error) error {
	if err == migrate.ErrNoChange {
		return nil
	}
	return err
}
