package database

import (
	"net/url"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func validMySQLEnv() map[string]string {
	specialPassword := strings.Join([]string{"test", "@:#/?", " space"}, "-")
	return map[string]string{
		"DB_HOST":     "2001:db8::1",
		"DB_PORT":     "3306",
		"DB_USER":     "weknora",
		"DB_PASSWORD": specialPassword,
		"DB_NAME":     "weknora_db",
	}
}

func TestBuildMySQLConfig(t *testing.T) {
	env := validMySQLEnv()
	cfg, err := BuildMySQLConfig(envMap(env))
	require.NoError(t, err)

	parsed, err := mysqlDriver.ParseDSN(cfg.GormDSN)
	require.NoError(t, err)
	assert.Equal(t, "weknora", parsed.User)
	assert.Equal(t, env["DB_PASSWORD"], parsed.Passwd)
	assert.Equal(t, "[2001:db8::1]:3306", parsed.Addr)
	assert.Equal(t, "weknora_db", parsed.DBName)
	assert.True(t, parsed.ParseTime)
	assert.Equal(t, time.UTC, parsed.Loc)
	assert.False(t, parsed.MultiStatements)

	assert.True(t, strings.HasPrefix(cfg.MigrationURL, "mysql://"))
	assert.Contains(t, cfg.MigrationURL, "multiStatements=true")
	credentialsEnd := strings.LastIndex(cfg.MigrationURL, "@tcp(")
	require.Positive(t, credentialsEnd)
	credentials := strings.TrimPrefix(cfg.MigrationURL[:credentialsEnd], "mysql://")
	credentialParts := strings.SplitN(credentials, ":", 2)
	require.Len(t, credentialParts, 2)
	migrationUser, err := url.QueryUnescape(credentialParts[0])
	require.NoError(t, err)
	migrationPassword, err := url.QueryUnescape(credentialParts[1])
	require.NoError(t, err)
	assert.Equal(t, "weknora", migrationUser)
	assert.Equal(t, env["DB_PASSWORD"], migrationPassword)
	assert.Equal(t, 50, cfg.MaxOpenConns)
	assert.Equal(t, 10, cfg.MaxIdleConns)
}

func TestBuildMySQLConfigValidation(t *testing.T) {
	for _, key := range []string{"DB_HOST", "DB_USER", "DB_NAME"} {
		values := validMySQLEnv()
		values[key] = ""
		_, err := BuildMySQLConfig(envMap(values))
		assert.Error(t, err, key)
	}

	values := validMySQLEnv()
	values["DB_PORT"] = "70000"
	_, err := BuildMySQLConfig(envMap(values))
	assert.Error(t, err)

	values = validMySQLEnv()
	values["DB_MAX_OPEN_CONNS"] = "5"
	values["DB_MAX_IDLE_CONNS"] = "10"
	_, err = BuildMySQLConfig(envMap(values))
	assert.Error(t, err)
}

func TestCheckMySQLVersion(t *testing.T) {
	for _, version := range []string{"8.0.16", "8.0.37", "8.4.0", "9.1.0"} {
		assert.NoError(t, CheckMySQLVersion(version), version)
	}
	for _, version := range []string{"", "5.7.44", "8.0.15", "10.11.8-MariaDB"} {
		assert.Error(t, CheckMySQLVersion(version), version)
	}
}

func TestValidateMySQLRetrieverConfig(t *testing.T) {
	assert.NoError(t, ValidateMySQLRetrieverConfig("mysql", "qdrant"))
	assert.NoError(t, ValidateMySQLRetrieverConfig("mysql", "elasticsearch_v7,qdrant"))
	for _, retrievers := range []string{"", "postgres", "sqlite", "mysql", "elasticsearch_v7", "qdrnat"} {
		assert.Error(t, ValidateMySQLRetrieverConfig("mysql", retrievers), retrievers)
	}
	assert.NoError(t, ValidateMySQLRetrieverConfig("postgres", "postgres"))
}
