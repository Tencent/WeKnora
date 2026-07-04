package container

import (
	"os"
	"strings"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMySQLDSNUsesDriverFormatter(t *testing.T) {
	dsn := buildMySQLDSN("user", "p@ss:word/with/slash", "db-host", "3307", "weknora")
	cfg, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(t, err)

	assert.Equal(t, "user", cfg.User)
	assert.Equal(t, "p@ss:word/with/slash", cfg.Passwd)
	assert.Equal(t, "tcp", cfg.Net)
	assert.Equal(t, "db-host:3307", cfg.Addr)
	assert.Equal(t, "weknora", cfg.DBName)
	assert.True(t, cfg.ParseTime)
	assert.True(t, cfg.InterpolateParams)
	assert.Contains(t, dsn, "charset=utf8mb4")
}

func TestBuildMySQLMigrateDSN(t *testing.T) {
	dsn := buildMySQLMigrateDSN("user", "secret", "db-host", "3306", "weknora")
	require.True(t, strings.HasPrefix(dsn, "mysql://"))

	cfg, err := mysqlDriver.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	require.NoError(t, err)
	assert.Equal(t, "weknora", cfg.DBName)
	assert.True(t, cfg.MultiStatements)
	assert.Contains(t, dsn, "charset=utf8mb4")
}

func TestMySQLEnvValueFallsBackToMainDBOnlyForMySQL(t *testing.T) {
	t.Setenv("MYSQL_DATABASE", "")
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DB_NAME", "main")
	assert.Equal(t, "fallback", mysqlEnvValue("MYSQL_DATABASE", "DB_NAME", "fallback"))

	t.Setenv("DB_DRIVER", "mysql")
	assert.Equal(t, "main", mysqlEnvValue("MYSQL_DATABASE", "DB_NAME", "fallback"))

	t.Setenv("MYSQL_DATABASE", "vector")
	assert.Equal(t, "vector", mysqlEnvValue("MYSQL_DATABASE", "DB_NAME", "fallback"))
}

func TestMySQLRetrieverConnectionOverrideSet(t *testing.T) {
	for _, key := range []string{"MYSQL_HOST", "MYSQL_PORT", "MYSQL_USERNAME", "MYSQL_PASSWORD", "MYSQL_DATABASE"} {
		t.Setenv(key, "")
	}

	assert.False(t, mysqlRetrieverConnectionOverrideSet())

	t.Setenv("MYSQL_TABLE_PREFIX", "custom")
	assert.False(t, mysqlRetrieverConnectionOverrideSet())

	t.Setenv("MYSQL_HOST", "mysql-vector")
	assert.True(t, mysqlRetrieverConnectionOverrideSet())
}

func TestMySQLRetrieverPasswordFallsBackToMainDBPassword(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_PASSWORD", "main-secret")
	t.Setenv("MYSQL_PASSWORD", "")

	assert.Equal(t, "main-secret", mysqlRetrieverPassword())

	t.Setenv("MYSQL_PASSWORD", "retriever-secret")
	assert.Equal(t, "retriever-secret", mysqlRetrieverPassword())
}

func TestNormalizeRetrieveDriverForMySQLPrimaryDB(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")

	t.Setenv("RETRIEVE_DRIVER", "")
	normalizeRetrieveDriverForPrimaryDB()
	assert.Equal(t, "mysql", strings.TrimSpace(os.Getenv("RETRIEVE_DRIVER")))

	t.Setenv("RETRIEVE_DRIVER", "postgres")
	normalizeRetrieveDriverForPrimaryDB()
	assert.Equal(t, "mysql", strings.TrimSpace(os.Getenv("RETRIEVE_DRIVER")))

	t.Setenv("RETRIEVE_DRIVER", "postgres,qdrant")
	normalizeRetrieveDriverForPrimaryDB()
	assert.Equal(t, "qdrant", strings.TrimSpace(os.Getenv("RETRIEVE_DRIVER")))

	t.Setenv("RETRIEVE_DRIVER", "mysql,postgres")
	normalizeRetrieveDriverForPrimaryDB()
	assert.Equal(t, "mysql", strings.TrimSpace(os.Getenv("RETRIEVE_DRIVER")))
}

func TestNormalizeRetrieveDriverTrimsAndDeduplicates(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("RETRIEVE_DRIVER", " postgres, qdrant, qdrant ")

	normalizeRetrieveDriverForPrimaryDB()

	assert.Equal(t, "qdrant", os.Getenv("RETRIEVE_DRIVER"))
}

func TestNormalizeRetrieveDriverKeepsPostgresPrimaryDBDefault(t *testing.T) {
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("RETRIEVE_DRIVER", "postgres")

	normalizeRetrieveDriverForPrimaryDB()

	assert.Equal(t, "postgres", strings.TrimSpace(os.Getenv("RETRIEVE_DRIVER")))
}
