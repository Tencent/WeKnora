package database

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/stretchr/testify/require"
)

// Two pull requests that each take the next migration number both pass their
// own checks and collide once both are merged: golang-migrate refuses to load
// a directory with a duplicate version, so every deployment fails to migrate.
// 000104 did exactly that. Loading each directory catches it, since pull
// request CI runs on the merge with the current main.
func TestMigrationDirectoriesLoad(t *testing.T) {
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	for _, dir := range []string{"versioned", "sqlite", "mysql"} {
		// Relative file sources avoid drive-letter URL parsing on Windows.
		src, err := source.Open("file://migrations/" + dir)
		require.NoError(t, err, "migrations/%s must load", dir)
		require.NoError(t, src.Close())
	}
}
