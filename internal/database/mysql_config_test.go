package database

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func mysqlTestEnv() map[string]string {
	return map[string]string{"DB_HOST": "::1", "DB_USER": "user+%", "DB_PASSWORD": "p+%@ss:/?#&", "DB_NAME": "weknora"}
}

func TestMySQLDSNsPreserveCredentialsAndSeparateMigrationOptions(t *testing.T) {
	env := mysqlTestEnv()
	app, migration, err := MySQLDSNs(func(key string) string { return env[key] })
	require.NoError(t, err)
	a, err := mysql.ParseDSN(app)
	require.NoError(t, err)
	require.Equal(t, "[::1]:3306", a.Addr)
	require.Equal(t, env["DB_USER"], a.User)
	require.Equal(t, env["DB_PASSWORD"], a.Passwd)
	require.False(t, a.MultiStatements)
	require.True(t, a.ParseTime)
	require.True(t, a.ClientFoundRows, "repository updates report matched rows, including unchanged values")
	require.Equal(t, time.UTC, a.Loc)
	require.Equal(t, "'+00:00'", a.Params["time_zone"])
	m, err := mysql.ParseDSN(strings.TrimPrefix(migration, "mysql://"))
	require.NoError(t, err)
	// golang-migrate unescapes credentials after mysql.ParseDSN.
	user, err := url.QueryUnescape(m.User)
	require.NoError(t, err)
	password, err := url.QueryUnescape(m.Passwd)
	require.NoError(t, err)
	require.Equal(t, a.User, user)
	require.Equal(t, a.Passwd, password)
	require.True(t, m.MultiStatements)
	require.Equal(t, a.DBName, m.DBName)
}

func TestMySQLDSNsRejectInvalidConfiguration(t *testing.T) {
	for _, key := range []string{"DB_HOST", "DB_USER", "DB_NAME"} {
		t.Run(key, func(t *testing.T) {
			env := mysqlTestEnv()
			env[key] = ""
			_, _, err := MySQLDSNs(func(k string) string { return env[k] })
			require.ErrorContains(t, err, key)
			require.NotContains(t, err.Error(), env["DB_PASSWORD"])
		})
	}
	for _, port := range []string{"bad", "0", "65536", "-1"} {
		env := mysqlTestEnv()
		env["DB_PORT"] = port
		_, _, err := MySQLDSNs(func(k string) string { return env[k] })
		require.ErrorContains(t, err, "DB_PORT")
	}
	for _, value := range []string{"invalid", "0s", "-1s"} {
		env := mysqlTestEnv()
		env["DB_CONNECT_TIMEOUT"] = value
		_, _, err := MySQLDSNs(func(k string) string { return env[k] })
		require.ErrorContains(t, err, "DB_CONNECT_TIMEOUT")
	}
}

func TestMySQLDSNsApplyTLSAndTimeouts(t *testing.T) {
	env := mysqlTestEnv()
	env["DB_MYSQL_TLS"] = "true"
	env["DB_CONNECT_TIMEOUT"] = "2s"
	env["DB_READ_TIMEOUT"] = "3s"
	env["DB_WRITE_TIMEOUT"] = "4s"
	app, _, err := MySQLDSNs(func(k string) string { return env[k] })
	require.NoError(t, err)
	cfg, err := mysql.ParseDSN(app)
	require.NoError(t, err)
	require.Equal(t, "true", cfg.TLSConfig)
	require.Equal(t, 2*time.Second, cfg.Timeout)
	require.Equal(t, 3*time.Second, cfg.ReadTimeout)
	require.Equal(t, 4*time.Second, cfg.WriteTimeout)
	env["DB_MYSQL_TLS"] = "skip-verify"
	_, _, err = MySQLDSNs(func(k string) string { return env[k] })
	require.ErrorContains(t, err, "DB_MYSQL_TLS")
}

func TestMySQLRetrieverConfigurationAllowsDynamicStores(t *testing.T) {
	for _, raw := range []string{"", "qdrant", " qdrant, opensearch "} {
		require.NoError(t, ValidateMySQLRetrievalDrivers(raw))
	}
	for _, raw := range []string{"postgres", "qdrant,sqlite", " POSTGRES "} {
		require.Error(t, ValidateMySQLRetrievalDrivers(raw))
	}
}
