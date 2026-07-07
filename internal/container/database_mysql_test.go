package container

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDatabaseConnectionSettingsMySQL(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "mysql")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_PASSWORD", "p@ss word#1")
	t.Setenv("DB_NAME", "WeKnora")

	settings, err := buildDatabaseConnectionSettingsFromEnv()
	require.NoError(t, err)
	require.Equal(t, "mysql", settings.Driver)
	require.Equal(t, "mysql", settings.Dialector.Name())
	require.Empty(t, settings.SQLiteDBPath)
	require.Contains(t, settings.MigrateDSN, "mysql://weknora:")
	require.Contains(t, settings.MigrateDSN, "@tcp(mysql:3306)/WeKnora")
	require.Contains(t, settings.MigrateDSN, "multiStatements=true")
	require.Contains(t, settings.MigrateDSN, "parseTime=true")
	require.Contains(t, settings.MigrateDSN, "loc=UTC")
}
