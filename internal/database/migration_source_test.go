package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Check both migration registries without requiring a live PostgreSQL server.
// Independently merged migrations must not reuse a version in either dialect.
func TestMigrationSourcesHaveUniqueVersions(t *testing.T) {
	root := sqliteRepoRoot(t)
	for _, dialect := range []string{"sqlite", "versioned"} {
		t.Run(dialect, func(t *testing.T) {
			// Inspect filenames directly. file:// source URLs are not portable
			// to Windows paths, and uniqueness is a filesystem-level property
			// of the migration registry.
			entries, err := os.ReadDir(filepath.Join(root, "migrations", dialect))
			require.NoError(t, err)
			seen := map[int]string{}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
					continue
				}
				var version int
				_, scanErr := fmt.Sscanf(entry.Name(), "%d_", &version)
				require.NoError(t, scanErr, "%s migration filename %s", dialect, entry.Name())
				if prior, exists := seen[version]; exists {
					t.Fatalf("%s migrations reuse version %d: %s and %s", dialect, version, prior, entry.Name())
				}
				seen[version] = entry.Name()
			}
			require.NotEmpty(t, seen)
		})
	}
}
