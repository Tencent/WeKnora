package database

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationPathForDSN(t *testing.T) {
	assert.Equal(t, "file://migrations/mysql", migrationPathForDSN("mysql://user:pass@tcp(mysql:3306)/weknora"))
	assert.Equal(t, "file://migrations/sqlite", migrationPathForDSN("sqlite3:///tmp/weknora.db"))
	assert.Equal(t, "file://migrations/versioned", migrationPathForDSN("postgres://postgres@postgres/weknora"))
}

func TestMigrationURLFromEnvMySQL(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "2001:db8::2")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "weknora-user")
	t.Setenv("DB_PASSWORD", "p@ss:#/? word")
	t.Setenv("DB_NAME", "weknora_db")

	dsn, err := migrationURLFromEnv()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(dsn, "mysql://"))

	config, err := gomysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	require.NoError(t, err)
	assert.Equal(t, "weknora-user", config.User)
	assert.Equal(t, "p@ss:#/? word", config.Passwd)
	assert.Equal(t, "[2001:db8::2]:3306", config.Addr)
	assert.Equal(t, "weknora_db", config.DBName)
	assert.True(t, config.ParseTime)
	assert.True(t, config.MultiStatements)
}

func TestMigrationURLFromEnvPostgresEscapesCredentials(t *testing.T) {
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DB_HOST", "2001:db8::3")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_USER", "weknora user")
	t.Setenv("DB_PASSWORD", "p@ss:#/? word")
	t.Setenv("DB_NAME", "weknora db")

	dsn, err := migrationURLFromEnv()
	require.NoError(t, err)
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	password, hasPassword := parsed.User.Password()
	assert.True(t, hasPassword)
	assert.Equal(t, "weknora user", parsed.User.Username())
	assert.Equal(t, "p@ss:#/? word", password)
	assert.Equal(t, "2001:db8::3", parsed.Hostname())
	assert.Equal(t, "5432", parsed.Port())
	assert.Equal(t, "/weknora db", parsed.Path)
	assert.Equal(t, "disable", parsed.Query().Get("sslmode"))
}

// TestMySQLMigrationRoundTrip is opt-in because it requires an existing,
// disposable empty database. The test applies the consolidated baseline with
// golang-migrate, verifies its version/table count, and rolls it back.
func TestMySQLMigrationRoundTrip(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_MYSQL_MIGRATION_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_MYSQL_MIGRATION_DSN is not configured")
	}
	require.True(t, strings.HasPrefix(dsn, "mysql://"))

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	originalWorkingDirectory, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() { _ = os.Chdir(originalWorkingDirectory) })

	require.NoError(t, RunMigrations(dsn))

	m, err := migrate.New("file://migrations/mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	version, dirty, err := m.Version()
	require.NoError(t, err)
	assert.Equal(t, uint(70), version)
	assert.False(t, dirty)

	db, err := sql.Open("mysql", strings.TrimPrefix(dsn, "mysql://"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var tableCount int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name <> 'schema_migrations'
	`).Scan(&tableCount))
	assert.Equal(t, 50, tableCount)

	require.NoError(t, m.Down())
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name <> 'schema_migrations'
	`).Scan(&tableCount))
	assert.Zero(t, tableCount)
}
