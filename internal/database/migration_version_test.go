package database

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationsHaveUniqueVersionNumbers(t *testing.T) {
	for _, directory := range []string{"versioned", "sqlite"} {
		t.Run(directory, func(t *testing.T) {
			assertUniqueMigrationVersions(t, "../../migrations/"+directory)
		})
	}
}

func assertUniqueMigrationVersions(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	pattern := regexp.MustCompile(`^([0-9]{6})_.+\.(up|down)\.sql$`)
	seen := make(map[string]map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, direction := match[1], match[2]
		if seen[version] == nil {
			seen[version] = make(map[string]string)
		}
		if previous := seen[version][direction]; previous != "" {
			t.Fatalf(
				"migration version %s has duplicate %s files: %s and %s",
				version,
				direction,
				previous,
				entry.Name(),
			)
		}
		seen[version][direction] = entry.Name()
	}
	for version, directions := range seen {
		require.NotEmpty(t, directions["up"], "migration %s is missing its up file", version)
		require.NotEmpty(t, directions["down"], "migration %s is missing its down file", version)
	}
}
