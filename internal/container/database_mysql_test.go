package container

import (
	"testing"

	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
)

func TestBuildDatabaseConnectionSettingsMySQL(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "mysql")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_PASSWORD", "p@ss word#1")
	t.Setenv("DB_NAME", "WeKnora")
	t.Setenv("RETRIEVE_DRIVER", "qdrant")

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
	dialector, ok := settings.Dialector.(*gormmysql.Dialector)
	require.True(t, ok)
	require.Contains(t, dialector.DSN, "weknora:p@ss word#1@tcp(mysql:3306)/WeKnora")
	require.Contains(t, dialector.DSN, "charset=utf8mb4")
	require.Contains(t, dialector.DSN, "parseTime=true")
	require.Contains(t, dialector.DSN, "loc=UTC")
}

func TestBuildDatabaseConnectionSettingsMySQLIPv6(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "[2001:db8::10]")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_NAME", "WeKnora")
	t.Setenv("RETRIEVE_DRIVER", "qdrant")

	settings, err := buildDatabaseConnectionSettingsFromEnv()
	require.NoError(t, err)
	dialector := settings.Dialector.(*gormmysql.Dialector)
	require.Contains(t, dialector.DSN, "tcp([2001:db8::10]:3306)")
	require.Contains(t, settings.MigrateDSN, "@tcp([2001:db8::10]:3306)")
}

func TestBuildDatabaseConnectionSettingsRejectsMySQLPostgresRetriever(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("RETRIEVE_DRIVER", "qdrant, postgres")

	_, err := buildDatabaseConnectionSettingsFromEnv()
	require.ErrorContains(t, err, "DB_DRIVER=mysql cannot use RETRIEVE_DRIVER=postgres")
}

func TestBuildDatabaseConnectionSettingsRequiresExternalMySQLRetriever(t *testing.T) {
	for _, retrieveDriver := range []string{"", "sqlite", "unknown"} {
		t.Run(retrieveDriver, func(t *testing.T) {
			t.Setenv("DB_DRIVER", "mysql")
			t.Setenv("RETRIEVE_DRIVER", retrieveDriver)
			_, err := buildDatabaseConnectionSettingsFromEnv()
			require.ErrorContains(t, err, "requires an external RETRIEVE_DRIVER")
		})
	}

	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "mysql")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("RETRIEVE_DRIVER", "unknown, qdrant")
	_, err := buildDatabaseConnectionSettingsFromEnv()
	require.NoError(t, err)
}

func TestBuildDatabaseConnectionSettingsExistingDrivers(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		t.Setenv("DB_DRIVER", "postgres")
		t.Setenv("DB_HOST", "postgres")
		t.Setenv("DB_PORT", "5432")
		t.Setenv("DB_USER", "postgres")
		t.Setenv("DB_PASSWORD", "secret")
		t.Setenv("DB_NAME", "WeKnora")
		settings, err := buildDatabaseConnectionSettingsFromEnv()
		require.NoError(t, err)
		require.Equal(t, "postgres", settings.Dialector.Name())
	})

	t.Run("sqlite", func(t *testing.T) {
		t.Setenv("DB_DRIVER", "sqlite")
		t.Setenv("DB_PATH", t.TempDir()+"/weknora.db")
		settings, err := buildDatabaseConnectionSettingsFromEnv()
		require.NoError(t, err)
		require.Equal(t, "sqlite", settings.Dialector.Name())
	})
}
