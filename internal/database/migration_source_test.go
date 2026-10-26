package database

import (
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

// Check migration registries without requiring a live database server.
// Independently merged migrations must not reuse a version within a dialect.
func TestMigrationSourcesHaveUniqueVersions(t *testing.T) {
	root := sqliteRepoRoot(t)
	for _, dialect := range []string{"sqlite", "versioned", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			migrations, err := iofs.New(os.DirFS(root), "migrations/"+dialect)
			require.NoError(t, err)
			require.NoError(t, migrations.Close())
		})
	}
}
