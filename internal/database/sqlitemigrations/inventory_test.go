package sqlitemigrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeMigrationFile(t *testing.T, directory string, name string, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644))
}

func writeMigrationPair(t *testing.T, directory string, version string, name string) {
	t.Helper()
	writeMigrationFile(t, directory, version+"_"+name+".up.sql", "-- up")
	writeMigrationFile(t, directory, version+"_"+name+".down.sql", "-- down")
}

func TestValidateDirectory(t *testing.T) {
	t.Run("complete and canonical order", func(t *testing.T) {
		directory := t.TempDir()
		writeMigrationPair(t, directory, "000001", "knowledge_folders")
		writeMigrationPair(t, directory, "000000", "init")
		writeMigrationFile(t, directory, "README.md", "documentation")

		inventory, err := ValidateDirectory(directory, 1)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"000000_init.up.sql",
			"000000_init.down.sql",
			"000001_knowledge_folders.up.sql",
			"000001_knowledge_folders.down.sql",
		}, inventory.Files)
	})

	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "missing up",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationFile(t, directory, "000001_knowledge_folders.down.sql", "-- down")
			},
		},
		{
			name: "missing down",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationFile(t, directory, "000001_knowledge_folders.up.sql", "-- up")
			},
		},
		{
			name: "version gap",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationPair(t, directory, "000002", "later")
			},
		},
		{
			name: "duplicate up",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationFile(t, directory, "000001_first.up.sql", "-- up")
				writeMigrationFile(t, directory, "000001_second.up.sql", "-- up")
				writeMigrationFile(t, directory, "000001_first.down.sql", "-- down")
			},
		},
		{
			name: "duplicate down",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationFile(t, directory, "000001_first.up.sql", "-- up")
				writeMigrationFile(t, directory, "000001_first.down.sql", "-- down")
				writeMigrationFile(t, directory, "000001_second.down.sql", "-- down")
			},
		},
		{
			name: "mismatched names",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationFile(t, directory, "000001_first.up.sql", "-- up")
				writeMigrationFile(t, directory, "000001_second.down.sql", "-- down")
			},
		},
		{
			name: "malformed filename",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationPair(t, directory, "000001", "knowledge_folders")
				writeMigrationFile(t, directory, "000002_invalid.side.sql", "-- invalid")
			},
		},
		{
			name: "empty file",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationFile(t, directory, "000001_knowledge_folders.up.sql", "")
				writeMigrationFile(t, directory, "000001_knowledge_folders.down.sql", "-- down")
			},
		},
		{
			name: "non regular file",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
				writeMigrationPair(t, directory, "000001", "knowledge_folders")
				require.NoError(t, os.Mkdir(
					filepath.Join(directory, "000002_directory.up.sql"),
					0o755,
				))
			},
		},
		{
			name: "required version missing",
			setup: func(t *testing.T, directory string) {
				writeMigrationPair(t, directory, "000000", "init")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			tt.setup(t, directory)
			_, err := ValidateDirectory(directory, 1)
			require.Error(t, err)
		})
	}
}

func TestValidateDirectoryIncludesFutureCompleteVersion(t *testing.T) {
	directory := t.TempDir()
	writeMigrationPair(t, directory, "000000", "init")
	writeMigrationPair(t, directory, "000001", "knowledge_folders")
	writeMigrationPair(t, directory, "000002", "future")

	inventory, err := ValidateDirectory(directory, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"000000_init.up.sql",
		"000000_init.down.sql",
		"000001_knowledge_folders.up.sql",
		"000001_knowledge_folders.down.sql",
		"000002_future.up.sql",
		"000002_future.down.sql",
	}, inventory.Files)
}
