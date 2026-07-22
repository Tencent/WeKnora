package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePostgresMigrationFile(
	t *testing.T,
	directory string,
	filename string,
	content string,
) {
	t.Helper()
	require.NoError(t, os.MkdirAll(directory, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(directory, filename),
		[]byte(content),
		0o644,
	))
}

func writePostgresMigrationPair(
	t *testing.T,
	directory string,
	version string,
	name string,
) {
	t.Helper()
	writePostgresMigrationFile(t, directory, version+"_"+name+".up.sql", "-- up")
	writePostgresMigrationFile(t, directory, version+"_"+name+".down.sql", "-- down")
}

func writeRequiredPostgresMigrationPair(t *testing.T, directory string) {
	t.Helper()
	writePostgresMigrationPair(
		t,
		directory,
		fmt.Sprintf("%06d", requiredPostgresMigrationVersion),
		requiredPostgresMigrationName,
	)
}

func postgresMigrationTestName(version uint) string {
	if version == requiredPostgresMigrationVersion {
		return requiredPostgresMigrationName
	}
	return fmt.Sprintf("migration_%06d", version)
}

func writePostgresMigrationRange(t *testing.T, directory string, maxVersion uint) {
	t.Helper()
	for version := uint(0); version <= maxVersion; version++ {
		writePostgresMigrationPair(
			t,
			directory,
			fmt.Sprintf("%06d", version),
			postgresMigrationTestName(version),
		)
	}
}

func postgresMigrationTestFilename(version uint, direction string) string {
	return fmt.Sprintf(
		"%06d_%s.%s.sql",
		version,
		postgresMigrationTestName(version),
		direction,
	)
}

func expectedPostgresMigrationFiles(maxVersion uint) []string {
	files := make([]string, 0, int(maxVersion+1)*2)
	for version := uint(0); version <= maxVersion; version++ {
		files = append(
			files,
			postgresMigrationTestFilename(version, "up"),
			postgresMigrationTestFilename(version, "down"),
		)
	}
	return files
}

func requiredPostgresMigrationFilename(direction string) string {
	return fmt.Sprintf(
		"%06d_%s.%s.sql",
		requiredPostgresMigrationVersion,
		requiredPostgresMigrationName,
		direction,
	)
}

func TestValidatePostgresMigrationsDirectory(t *testing.T) {
	t.Run("complete version chain through required migration", func(t *testing.T) {
		directory := t.TempDir()
		writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)

		inventory, err := validatePostgresMigrationsDirectory(directory)
		require.NoError(t, err)
		expectedVersionCount := int(requiredPostgresMigrationVersion + 1)
		assert.Len(t, inventory.versions, expectedVersionCount)
		assert.Len(t, inventory.files, expectedVersionCount*2)
		assert.Equal(t, uint(0), inventory.versions[0])
		assert.Equal(t, requiredPostgresMigrationVersion, inventory.versions[len(inventory.versions)-1])
		assert.Equal(t, expectedPostgresMigrationFiles(requiredPostgresMigrationVersion), inventory.files)
	})

	tests := []struct {
		name          string
		setup         func(*testing.T, string)
		errorContains string
	}{
		{
			name: "required pair missing with highest version seventy",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion-1)
			},
			errorContains: "required postgres migration version 000071",
		},
		{
			name: "only required pair is missing historical versions",
			setup: func(t *testing.T, directory string) {
				writeRequiredPostgresMigrationPair(t, directory)
			},
			errorContains: "000000",
		},
		{
			name: "entire historical pair missing",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					postgresMigrationTestFilename(35, "up"),
				)))
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					postgresMigrationTestFilename(35, "down"),
				)))
			},
			errorContains: "000035",
		},
		{
			name: "required up missing",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					requiredPostgresMigrationFilename("up"),
				)))
			},
		},
		{
			name: "required down missing",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					requiredPostgresMigrationFilename("down"),
				)))
			},
		},
		{
			name: "required names mismatch",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					requiredPostgresMigrationFilename("down"),
				)))
				writePostgresMigrationFile(
					t,
					directory,
					fmt.Sprintf("%06d_other.down.sql", requiredPostgresMigrationVersion),
					"-- down",
				)
			},
		},
		{
			name: "required name is wrong",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion-1)
				writePostgresMigrationPair(
					t,
					directory,
					fmt.Sprintf("%06d", requiredPostgresMigrationVersion),
					"other",
				)
			},
		},
		{
			name: "empty up",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				writePostgresMigrationFile(
					t,
					directory,
					requiredPostgresMigrationFilename("up"),
					"",
				)
			},
		},
		{
			name: "empty down",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				writePostgresMigrationFile(
					t,
					directory,
					requiredPostgresMigrationFilename("down"),
					"",
				)
			},
		},
		{
			name: "directory masquerades as migration",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				require.NoError(t, os.Remove(filepath.Join(
					directory,
					requiredPostgresMigrationFilename("up"),
				)))
				require.NoError(t, os.Mkdir(
					filepath.Join(directory, requiredPostgresMigrationFilename("up")),
					0o755,
				))
			},
		},
		{
			name: "malformed migration filename",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				writePostgresMigrationFile(t, directory, "000072_future.side.sql", "-- invalid")
			},
		},
		{
			name: "recognized version has only up",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				writePostgresMigrationFile(t, directory, "000072_future.up.sql", "-- up")
			},
		},
		{
			name: "recognized version has only down",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
				writePostgresMigrationFile(t, directory, "000072_future.down.sql", "-- down")
			},
		},
		{
			name: "duplicate up",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion+1)
				writePostgresMigrationFile(t, directory, "000072_zz_duplicate.up.sql", "-- up")
			},
			errorContains: "duplicate up",
		},
		{
			name: "duplicate down",
			setup: func(t *testing.T, directory string) {
				writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion+1)
				writePostgresMigrationFile(t, directory, "000072_zz_duplicate.down.sql", "-- down")
			},
			errorContains: "duplicate down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			tt.setup(t, directory)
			_, err := validatePostgresMigrationsDirectory(directory)
			require.Error(t, err)
			if tt.errorContains != "" {
				assert.Contains(t, err.Error(), tt.errorContains)
			}
		})
	}
}

func TestValidatePostgresMigrationsDirectoryRejectsInvalidDirectory(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, err := validatePostgresMigrationsDirectory(filepath.Join(t.TempDir(), "missing"))
		require.Error(t, err)
	})

	t.Run("not a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "migrations")
		require.NoError(t, os.WriteFile(path, []byte("not a directory"), 0o644))

		_, err := validatePostgresMigrationsDirectory(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}

func TestValidatePostgresMigrationsDirectoryRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	writePostgresMigrationFile(
		t,
		directory,
		requiredPostgresMigrationFilename("down"),
		"-- down",
	)
	target := filepath.Join(t.TempDir(), "migration.sql")
	require.NoError(t, os.WriteFile(target, []byte("-- up"), 0o644))
	if err := os.Symlink(
		target,
		filepath.Join(directory, requiredPostgresMigrationFilename("up")),
	); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}

	_, err := validatePostgresMigrationsDirectory(directory)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestValidatePostgresMigrationsDirectoryRejectsFutureVersionGap(t *testing.T) {
	directory := t.TempDir()
	writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
	writePostgresMigrationPair(t, directory, "000073", "future")

	_, err := validatePostgresMigrationsDirectory(directory)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "000072")
}

func TestValidatePostgresMigrationsDirectoryAcceptsCompleteFutureVersion(t *testing.T) {
	directory := t.TempDir()
	writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion+1)

	inventory, err := validatePostgresMigrationsDirectory(directory)
	require.NoError(t, err)
	maxVersion := requiredPostgresMigrationVersion + 1
	assert.Len(t, inventory.versions, int(maxVersion+1))
	assert.Len(t, inventory.files, int(maxVersion+1)*2)
	assert.Equal(t, maxVersion, inventory.versions[len(inventory.versions)-1])
	assert.Equal(t, expectedPostgresMigrationFiles(maxVersion), inventory.files)
}

func TestValidatePostgresMigrationsDirectoryCurrentSource(t *testing.T) {
	directory := filepath.Join(knowledgeFolderMigrationRoot(t), "migrations", "versioned")

	inventory, err := validatePostgresMigrationsDirectory(directory)
	require.NoError(t, err)
	expectedVersionCount := int(requiredPostgresMigrationVersion + 1)
	assert.Len(t, inventory.versions, expectedVersionCount)
	assert.Len(t, inventory.files, expectedVersionCount*2)
	assert.Equal(t, uint(0), inventory.versions[0])
	assert.Equal(t, requiredPostgresMigrationVersion, inventory.versions[len(inventory.versions)-1])
}

func TestNewValidatedPostgresMigratorStopsBeforeFactoryOnInvalidSource(t *testing.T) {
	directory := t.TempDir()
	writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion-1)
	factoryCalls := 0
	databaseURL := "postgres://must-not-be-used"
	factoryErr := errors.New("factory must not be called")

	_, err := newValidatedPostgresMigrator(
		directory,
		databaseURL,
		func(sourceURL string, gotDatabaseURL string) (*migrate.Migrate, error) {
			factoryCalls++
			return nil, factoryErr
		},
	)

	require.Error(t, err)
	assert.Zero(t, factoryCalls)
	assert.False(t, errors.Is(err, factoryErr))
	assert.NotContains(t, err.Error(), databaseURL)
}

func TestNewValidatedPostgresMigratorCallsFactoryAfterValidation(t *testing.T) {
	directory := t.TempDir()
	writePostgresMigrationRange(t, directory, requiredPostgresMigrationVersion)
	absoluteDirectory, err := filepath.Abs(directory)
	require.NoError(t, err)
	factoryCalls := 0
	factoryErr := errors.New("factory reached")

	_, err = newValidatedPostgresMigrator(
		directory,
		"postgres://test",
		func(sourceURL string, databaseURL string) (*migrate.Migrate, error) {
			factoryCalls++
			assert.Equal(t, postgresMigrationSourceURL(absoluteDirectory), sourceURL)
			assert.Equal(t, "postgres://test", databaseURL)
			return nil, factoryErr
		},
	)

	require.ErrorIs(t, err, factoryErr)
	assert.Equal(t, 1, factoryCalls)
}
