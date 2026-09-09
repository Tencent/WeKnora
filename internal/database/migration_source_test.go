package database

import (
	"io"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

func TestMigrationSourcesHaveUniqueVersions(t *testing.T) {
	for _, directory := range []string{"versioned", "sqlite"} {
		t.Run(directory, func(t *testing.T) {
			driver, err := iofs.New(os.DirFS("../../migrations"), directory)
			require.NoError(t, err, "migration versions and directions must be unique")
			t.Cleanup(func() { require.NoError(t, driver.Close()) })
			version := uint(14)
			if directory == "versioned" {
				version, err = driver.Next(92)
				require.NoError(t, err)
				require.Equal(t, uint(93), version)
				reader, identifier, err := driver.ReadUp(92)
				require.NoError(t, err)
				require.Equal(t, "mcp_metadata", identifier)
				require.NoError(t, reader.Close())
			}
			for _, read := range []func(uint) (io.ReadCloser, string, error){driver.ReadUp, driver.ReadDown} {
				reader, identifier, err := read(version)
				require.NoError(t, err)
				require.Equal(t, "learning", identifier)
				body, err := io.ReadAll(reader)
				require.NoError(t, reader.Close())
				require.NoError(t, err)
				require.NotEmpty(t, body)
			}
		})
	}
}
