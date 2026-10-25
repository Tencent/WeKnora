//go:build mysqlintegration

package container

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func mysqlContainerEnv(t *testing.T, migrate bool) string {
	t.Helper()
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	require.NotEmpty(t, dsn, "mysqlintegration requires a disposable MySQL test DSN")
	cfg, err := mysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	require.NoError(t, err)
	require.Contains(t, cfg.DBName, "test")
	adminCfg := cfg.Clone()
	adminCfg.DBName = ""
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	require.NoError(t, err)
	name := fmt.Sprintf("weknora_mysql_test_container_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := admin.Exec("DROP DATABASE `" + name + "`")
		require.NoError(t, dropErr)
		require.NoError(t, admin.Close())
	})
	parts := strings.Split(cfg.Addr, ":")
	require.Len(t, parts, 2, "container test DSN must use IPv4 or a hostname")
	for key, value := range map[string]string{
		"DB_DRIVER": "mysql", "DB_HOST": parts[0], "DB_PORT": parts[1],
		"DB_USER": cfg.User, "DB_PASSWORD": cfg.Passwd, "DB_NAME": name,
		"RETRIEVE_DRIVER": "", "AUTO_MIGRATE": fmt.Sprint(migrate), "DB_MYSQL_TLS": "false",
	} {
		t.Setenv(key, value)
	}
	return name
}

func TestMySQLDatabaseConnectsWithoutAnEnvironmentVectorStore(t *testing.T) {
	mysqlContainerEnv(t, false)
	db, err := initDatabase(&config.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.Equal(t, "mysql", db.Name())
	var zone string
	require.NoError(t, db.Raw("SELECT @@session.time_zone").Scan(&zone).Error)
	require.Equal(t, "+00:00", zone)
	for _, engine := range []types.RetrieverEngineType{types.PostgresRetrieverEngineType, types.SQLiteRetrieverEngineType} {
		_, err := createEngineServiceFromStore(context.Background(), types.VectorStore{EngineType: engine, ConnectionConfig: types.ConnectionConfig{UseDefaultConnection: true}}, db, &config.Config{}, nil)
		require.ErrorContains(t, err, "mysql")
	}
}

func TestMySQLDatabaseMigratesFreshSchema(t *testing.T) {
	mysqlContainerEnv(t, true)
	mysqlContainerChdirRoot(t)
	db, err := initDatabase(&config.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	var version int
	var dirty bool
	require.NoError(t, db.Raw("SELECT version, dirty FROM schema_migrations").Row().Scan(&version, &dirty))
	require.Zero(t, version)
	require.False(t, dirty)
	for _, table := range []string{"sessions", "memory_items", "agent_prompts", "agent_grants", "agent_collaborations", "automations", "session_sandbox_assignments"} {
		var exists int
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table).Scan(&exists).Error)
		require.Equal(t, 1, exists, table)
	}
}

func mysqlContainerChdirRoot(t *testing.T) {
	t.Helper()
	root, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		require.NotEqual(t, root, parent)
		root = parent
	}
	t.Chdir(root)
}

func TestMySQLDatabaseRejectsSharedConnectionRetrievers(t *testing.T) {
	mysqlContainerEnv(t, false)
	for _, driver := range []string{"postgres", "sqlite", "qdrant,postgres"} {
		t.Setenv("RETRIEVE_DRIVER", driver)
		_, err := initDatabase(&config.Config{})
		require.ErrorContains(t, err, "RETRIEVE_DRIVER")
	}
}

func TestMySQLDatabaseMigrationFailureStopsStartup(t *testing.T) {
	name := mysqlContainerEnv(t, true)
	mysqlContainerChdirRoot(t)
	t.Setenv("AUTO_RECOVER_DIRTY", "true")
	adminCfg, err := mysql.ParseDSN(strings.TrimPrefix(os.Getenv("WEKNORA_MYSQL_TEST_DSN"), "mysql://"))
	require.NoError(t, err)
	adminCfg.DBName = ""
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	_, err = admin.Exec("CREATE TABLE `" + name + "`.schema_migrations (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL)")
	require.NoError(t, err)
	_, err = admin.Exec("INSERT INTO `" + name + "`.schema_migrations VALUES (0, true)")
	require.NoError(t, err)
	_, err = initDatabase(&config.Config{})
	require.ErrorContains(t, err, "dirty")
	var dirty bool
	require.NoError(t, admin.QueryRowContext(context.Background(), "SELECT dirty FROM `"+name+"`.schema_migrations").Scan(&dirty))
	require.True(t, dirty)
}

func TestMySQLStartupResolvesOnlyPendingStorageProviders(t *testing.T) {
	db := testutil.MySQL(t)
	t.Setenv("STORAGE_TYPE", "local")
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases (id,tenant_id,name,embedding_model_id,summary_model_id,storage_provider_config) VALUES ('pending',1,'pending','','',?),('selected',1,'selected','','',?),('unset',1,'unset','','',NULL)", `{"provider":"__pending_env__"}`, `{"provider":"s3"}`).Error)
	resolveStorageProviderPending(db)
	var provider string
	require.NoError(t, db.Raw("SELECT JSON_UNQUOTE(JSON_EXTRACT(storage_provider_config,'$.provider')) FROM knowledge_bases WHERE id='pending'").Scan(&provider).Error)
	require.Equal(t, "local", provider)
	require.NoError(t, db.Raw("SELECT JSON_UNQUOTE(JSON_EXTRACT(storage_provider_config,'$.provider')) FROM knowledge_bases WHERE id='selected'").Scan(&provider).Error)
	require.Equal(t, "s3", provider)
	var unset bool
	require.NoError(t, db.Raw("SELECT storage_provider_config IS NULL FROM knowledge_bases WHERE id='unset'").Scan(&unset).Error)
	require.True(t, unset)
}
