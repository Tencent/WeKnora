package database

import (
	"io"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
)

func TestMigrationSourcesHaveUniqueVersions(t *testing.T) {
	for _, test := range []struct {
		directory  string
		previous   uint
		identifier string
	}{
		{directory: "versioned", previous: 92, identifier: "mcp_metadata"},
		{directory: "sqlite", previous: 13, identifier: "mcp_tool_enabled"},
	} {
		t.Run(test.directory, func(t *testing.T) {
			driver, err := iofs.New(os.DirFS("../../migrations"), test.directory)
			require.NoError(t, err, "migration versions and directions must be unique")
			t.Cleanup(func() { require.NoError(t, driver.Close()) })
			for offset, wantIdentifier := range []string{
				test.identifier,
				"browser_authorization",
				"learning",
				"learning_credit_source",
			} {
				version := test.previous + uint(offset)
				if offset > 0 {
					next, err := driver.Next(version - 1)
					require.NoError(t, err)
					require.Equal(t, version, next)
					previous, err := driver.Prev(version)
					require.NoError(t, err)
					require.Equal(t, version-1, previous)
				}
				for _, read := range []func(uint) (io.ReadCloser, string, error){driver.ReadUp, driver.ReadDown} {
					reader, identifier, err := read(version)
					require.NoError(t, err)
					require.Equal(t, wantIdentifier, identifier)
					body, err := io.ReadAll(reader)
					require.NoError(t, reader.Close())
					require.NoError(t, err)
					require.NotEmpty(t, body)
				}
			}
		})
	}
}
