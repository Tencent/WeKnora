package database

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

var migrationFilenamePattern = regexp.MustCompile(`^([0-9]+)_.+\.(up|down)\.sql$`)

func TestMigrationVersionsAreUniqueAndPaired(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration test path")
	}
	migrationsRoot := filepath.Join(filepath.Dir(filename), "..", "..", "migrations")

	for _, dialect := range []string{"versioned", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(migrationsRoot, dialect))
			if err != nil {
				t.Fatalf("read %s migrations: %v", dialect, err)
			}

			filesByVersion := make(map[string]map[string]string)
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				matches := migrationFilenamePattern.FindStringSubmatch(entry.Name())
				if matches == nil {
					continue
				}
				version, direction := matches[1], matches[2]
				if filesByVersion[version] == nil {
					filesByVersion[version] = make(map[string]string)
				}
				if previous := filesByVersion[version][direction]; previous != "" {
					t.Errorf(
						"migration version %s has duplicate %s files: %s and %s",
						version,
						direction,
						previous,
						entry.Name(),
					)
				}
				filesByVersion[version][direction] = entry.Name()
			}

			for version, directions := range filesByVersion {
				for _, direction := range []string{"up", "down"} {
					if directions[direction] == "" {
						t.Errorf("migration version %s is missing its %s file", version, direction)
					}
				}
			}
		})
	}
}
