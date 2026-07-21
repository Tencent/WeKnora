package container

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMySQLDSNsPreservesCredentialsAndOptions(t *testing.T) {
	gormDSN, migrationDSN := buildMySQLDSNs(
		"2001:db8::1",
		"3306",
		"weknora-user",
		"p@ss:#/? word",
		"weknora_db",
	)

	gormConfig, err := gomysql.ParseDSN(gormDSN)
	require.NoError(t, err)
	assert.Equal(t, "weknora-user", gormConfig.User)
	assert.Equal(t, "p@ss:#/? word", gormConfig.Passwd)
	assert.Equal(t, "[2001:db8::1]:3306", gormConfig.Addr)
	assert.Equal(t, "weknora_db", gormConfig.DBName)
	assert.True(t, gormConfig.ParseTime)
	assert.False(t, gormConfig.MultiStatements)
	assert.Contains(t, gormDSN, "charset=utf8mb4")

	require.True(t, strings.HasPrefix(migrationDSN, "mysql://"))
	migrationConfig, err := gomysql.ParseDSN(strings.TrimPrefix(migrationDSN, "mysql://"))
	require.NoError(t, err)
	assert.Equal(t, gormConfig.User, migrationConfig.User)
	assert.Equal(t, gormConfig.Passwd, migrationConfig.Passwd)
	assert.True(t, migrationConfig.MultiStatements)
}

func TestValidateDatabaseRetrieverConfig(t *testing.T) {
	tests := []struct {
		name       string
		dbDriver   string
		retrievers string
		wantError  string
	}{
		{name: "postgres unchanged", dbDriver: "postgres", retrievers: "postgres"},
		{name: "mysql with qdrant", dbDriver: "mysql", retrievers: "qdrant"},
		{name: "mysql with multiple external stores", dbDriver: "mysql", retrievers: "qdrant,elasticsearch_v8"},
		{name: "mysql rejects postgres vector store", dbDriver: "mysql", retrievers: "qdrant, postgres", wantError: "DB_DRIVER=mysql"},
		{name: "mysql is not a vector store", dbDriver: "mysql", retrievers: "mysql", wantError: "RETRIEVE_DRIVER=mysql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDatabaseRetrieverConfig(tt.dbDriver, tt.retrievers)
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

// TestInitDatabaseMySQL is opt-in because it requires a disposable empty
// MySQL database. It covers startup migration and all post-migration hooks,
// then rolls the business schema back before returning.
func TestInitDatabaseMySQL(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_MYSQL_STARTUP_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_MYSQL_STARTUP_DSN is not configured")
	}
	parsed, err := gomysql.ParseDSN(dsn)
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(parsed.Addr)
	require.NoError(t, err)

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	originalWorkingDirectory, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() { _ = os.Chdir(originalWorkingDirectory) })

	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", host)
	t.Setenv("DB_PORT", port)
	t.Setenv("DB_USER", parsed.User)
	t.Setenv("DB_PASSWORD", parsed.Passwd)
	t.Setenv("DB_NAME", parsed.DBName)
	t.Setenv("RETRIEVE_DRIVER", "qdrant")
	t.Setenv("AUTO_MIGRATE", "true")
	t.Setenv("AUTO_RECOVER_DIRTY", "false")
	t.Setenv("STORAGE_TYPE", "local")

	db, err := initDatabase(&config.Config{})
	require.NoError(t, err)
	var tableCount int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name <> 'schema_migrations'
	`).Scan(&tableCount).Error)
	assert.Equal(t, int64(50), tableCount)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	parsed.MultiStatements = true
	migrationDSN := "mysql://" + parsed.FormatDSN()
	m, err := migrate.New("file://migrations/mysql", migrationDSN)
	require.NoError(t, err)
	require.NoError(t, m.Down())
	_, _ = m.Close()
}
