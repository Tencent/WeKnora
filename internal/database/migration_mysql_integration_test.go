package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// TestRunMigrationsMySQL exercises the same golang-migrate entry point used at
// application startup against a disposable, initially empty MySQL database.
// It is opt-in because it drops no objects and requires an isolated database.
//
// MYSQL_MIGRATION_TEST_DSN uses golang-migrate's URL form, for example:
// mysql://user:pass@tcp(127.0.0.1:3306)/weknora_migration_test?multiStatements=true
// MYSQL_TEST_DSN uses go-sql-driver's form for post-migration assertions.
func TestRunMigrationsMySQL(t *testing.T) {
	migrationDSN := os.Getenv("MYSQL_MIGRATION_TEST_DSN")
	queryDSN := os.Getenv("MYSQL_TEST_DSN")
	if migrationDSN == "" || queryDSN == "" {
		t.Skip("MYSQL_MIGRATION_TEST_DSN and MYSQL_TEST_DSN are not set")
	}

	oldWorkingDir, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err := filepath.Abs(filepath.Join(oldWorkingDir, "..", ".."))
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWorkingDir)) })

	require.NoError(t, RunMigrations(migrationDSN))
	version, dirty, ok := CachedMigrationVersion()
	require.True(t, ok)
	require.EqualValues(t, 65, version)
	require.False(t, dirty)
	require.Empty(t, CachedMigrationError())

	db, err := sql.Open("mysql", queryDSN)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Ping())

	for _, table := range []string{"tenants", "knowledge_bases", "chunks", "tenant_api_keys"} {
		var count int
		require.NoError(t, db.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			table,
		).Scan(&count))
		require.Equal(t, 1, count, "expected migration to create table %s", table)
	}

	// A second startup must be a clean no-op.
	require.NoError(t, RunMigrations(migrationDSN))
}
