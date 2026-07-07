package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationSourceForDSNUsesMySQLDirectory(t *testing.T) {
	require.Equal(t, "file://migrations/mysql", migrationSourceForDSN("mysql://user:pass@tcp(mysql:3306)/WeKnora"))
	require.Equal(t, "file://migrations/versioned", migrationSourceForDSN("postgres://user:pass@postgres:5432/WeKnora"))
	require.Equal(t, "file://migrations/sqlite", migrationSourceForDSN("sqlite3://data/weknora.db"))
}
