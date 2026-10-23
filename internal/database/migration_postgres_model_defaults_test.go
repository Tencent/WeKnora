package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

func TestPostgresModelDefaultsUpgradePreservesLegacySelection(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	m, err := migrate.New("file://migrations/versioned", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, ignoreNoChange(m.Migrate(versionedMigrationBefore(t, root, 134))))
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	want := seedModelDefaultHistory(t, db)
	before := modelDefaultHistorySnapshot(t, db)
	require.NoError(t, RunMigrationsWithOptions(dsn, MigrationOptions{}))
	require.Equal(t, before, modelDefaultHistorySnapshot(t, db), "migration must preserve model rows and credentials")
	migrationPath := filepath.Join(root, "migrations/versioned/000134_model_defaults.up.sql")
	assertMigratedModelDefaults(t, db, "postgres", migrationPath, want)
}
