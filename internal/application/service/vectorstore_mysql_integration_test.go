package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMySQLVectorStoreConnectionIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN to run the real MySQL connection test")
	}
	cfg, err := gomysql.ParseDSN(dsn)
	require.NoError(t, err)

	username := cfg.User
	if strings.EqualFold(username, "root") {
		// Exercise the public connection-config default used by VectorStore.
		username = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	version, err := testMySQLConnection(ctx, types.ConnectionConfig{
		Addr:     cfg.Addr,
		Username: username,
		Password: cfg.Passwd,
		Database: cfg.DBName,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, version)
}
