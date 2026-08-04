package database

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigDefaultsMySQLPortAndTimeouts(t *testing.T) {
	cfg, err := LoadConfig(func(key string) string {
		values := map[string]string{
			"DB_DRIVER":   "mysql",
			"DB_HOST":     "mysql.local",
			"DB_USER":     "weknora",
			"DB_PASSWORD": "secret",
			"DB_NAME":     "weknora",
		}
		return values[key]
	})

	require.NoError(t, err)
	require.Equal(t, DriverMySQL, cfg.Driver)
	require.Equal(t, 3306, cfg.Port)
	require.Equal(t, 5*time.Second, cfg.ConnectTimeout)
	require.Equal(t, 30*time.Second, cfg.ReadTimeout)
	require.Equal(t, 30*time.Second, cfg.WriteTimeout)
}

func TestMySQLDSNsUseDriverConfigAndDoNotLeakPasswordInSafeSummary(t *testing.T) {
	cfg := Config{
		Driver:          DriverMySQL,
		Host:            "2001:db8::1",
		Port:            3306,
		User:            "weknora",
		Password:        "p@ss word/with:specials",
		Name:            "weknora",
		ConnectTimeout:  7 * time.Second,
		ReadTimeout:     8 * time.Second,
		WriteTimeout:    9 * time.Second,
		MaxOpenConns:    20,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 5 * time.Minute,
	}

	appDSN, err := MySQLApplicationDSN(cfg)
	require.NoError(t, err)
	require.Contains(t, appDSN, "tcp([2001:db8::1]:3306)")
	require.Contains(t, appDSN, "parseTime=true")
	require.Contains(t, appDSN, "loc=UTC")
	require.Contains(t, appDSN, "charset=utf8mb4")
	require.Contains(t, appDSN, "timeout=7s")
	require.NotContains(t, appDSN, "multiStatements=true")

	migrationDSN, err := MySQLMigrationDSN(cfg)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(migrationDSN, "mysql://"))
	require.Contains(t, migrationDSN, "multiStatements=true")

	summary := cfg.SafeSummary()
	require.Contains(t, summary, "driver=mysql")
	require.Contains(t, summary, "host=2001:db8::1")
	require.NotContains(t, summary, cfg.Password)

	parsed, err := url.ParseQuery(strings.SplitN(migrationDSN, "?", 2)[1])
	require.NoError(t, err)
	require.Equal(t, "true", parsed.Get("multiStatements"))
}

func TestValidateDriverCombinationForMySQL(t *testing.T) {
	require.NoError(t, ValidateDriverCombination(DriverMySQL, []string{" qdrant ", "qdrant", "opensearch"}))

	err := ValidateDriverCombination(DriverMySQL, []string{"postgres"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB_DRIVER=mysql")
	require.Contains(t, err.Error(), "postgres")

	err = ValidateDriverCombination(DriverMySQL, []string{"not-a-driver"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown RETRIEVE_DRIVER")

	err = ValidateDriverCombination(DriverMySQL, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires at least one external vector retriever")
}
