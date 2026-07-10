package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationSourceForDSNUsesMySQLDirectory(t *testing.T) {
	tests := []struct {
		dsn  string
		want string
	}{
		{"mysql://user:pass@tcp(mysql:3306)/WeKnora", "file://migrations/mysql"},
		{"postgres://user:pass@postgres:5432/WeKnora", "file://migrations/versioned"},
		{"sqlite3://data/weknora.db", "file://migrations/sqlite"},
	}
	for _, test := range tests {
		got, err := migrationSourceForDSN(test.dsn)
		require.NoError(t, err)
		require.Equal(t, test.want, got)
	}

	_, err := migrationSourceForDSN("oracle://db.example/WeKnora")
	require.ErrorContains(t, err, "unsupported migration database DSN")
}

func TestMigrationDSNFromEnvMySQLSpecialPasswordAndIPv6(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "[2001:db8::10]")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "user@tenant")
	t.Setenv("DB_PASSWORD", "p@ss:/?# word")
	t.Setenv("DB_NAME", "WeKnora")

	dsn, err := migrationDSNFromEnv()
	require.NoError(t, err)
	require.Contains(t, dsn, "user%40tenant:p%40ss%3A%2F%3F%23%20word")
	require.Contains(t, dsn, "@tcp([2001:db8::10]:3306)/WeKnora")
	require.Contains(t, dsn, "multiStatements=true")
}

func TestMigrationDSNFromEnvRejectsUnknownDriver(t *testing.T) {
	t.Setenv("DB_DRIVER", "oracle")
	_, err := migrationDSNFromEnv()
	require.ErrorContains(t, err, "unsupported database driver")
}
