package database

import (
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4/source"
	"github.com/stretchr/testify/require"
)

// Check both migration registries without requiring a live PostgreSQL server.
// Independently merged migrations must not reuse a version in either dialect.
func TestMigrationSourcesHaveUniqueVersions(t *testing.T) {
	root := sqliteRepoRoot(t)
	for _, dialect := range []string{"sqlite", "versioned"} {
		t.Run(dialect, func(t *testing.T) {
			migrations, err := source.Open("file://" + filepath.Join(root, "migrations", dialect))
			require.NoError(t, err)
			require.NoError(t, migrations.Close())
		})
	}
}
