//go:build mysqlintegration

package database

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

func mysqlMigrationDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Fatal("WEKNORA_MYSQL_TEST_DSN is required for mysqlintegration tests")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(cfg.DBName), "test", "integration database name must contain test")
	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	require.NoError(t, err)
	name := fmt.Sprintf("weknora_migration_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE `" + name + "`"); _ = admin.Close() })
	cfg.DBName = name
	cfg.MultiStatements = true
	cfg.ParseTime = true
	cfg.Params = map[string]string{"time_zone": "'+00:00'"}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	migrationCfg := cfg.Clone()
	migrationCfg.User = url.QueryEscape(cfg.User)
	migrationCfg.Passwd = url.QueryEscape(cfg.Passwd)
	return db, "mysql://" + migrationCfg.FormatDSN()
}

func TestMySQLMigrationsLifecycle(t *testing.T) {
	db, dsn := mysqlMigrationDB(t)
	chdirAndRestore(t, sqliteRepoRoot(t))
	require.NoError(t, RunMigrations(dsn))
	require.NoError(t, RunMigrations(dsn))
	cfg, err := mysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	require.NoError(t, err)
	cfg.User, err = url.QueryUnescape(cfg.User)
	require.NoError(t, err)
	cfg.Passwd, err = url.QueryUnescape(cfg.Passwd)
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(cfg.Addr)
	require.NoError(t, err)
	for key, value := range map[string]string{"DB_DRIVER": "mysql", "DB_HOST": host, "DB_PORT": port, "DB_USER": cfg.User, "DB_PASSWORD": cfg.Passwd, "DB_NAME": cfg.DBName, "DB_MYSQL_TLS": "false"} {
		t.Setenv(key, value)
	}
	version, dirty, err := GetMigrationVersion()
	require.NoError(t, err)
	require.Zero(t, version)
	require.False(t, dirty)
	for _, table := range append(versionedSQLiteTables, "tenants", "memory_items", "tenant_api_keys", "resources") {
		var n int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table).Scan(&n))
		require.Equal(t, 1, n, table)
	}
	for table, cols := range versionedSQLiteColumns {
		for _, col := range cols {
			var n int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?", table, col).Scan(&n))
			require.Equal(t, 1, n, table+"."+col)
		}
	}
	var typ string
	require.NoError(t, db.QueryRow("SELECT column_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='messages' AND column_name='attachments'").Scan(&typ))
	require.Equal(t, "json", typ)
	require.NoError(t, db.QueryRow("SELECT column_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='memory_item_embeddings' AND column_name='vector'").Scan(&typ))
	require.Equal(t, "blob", typ)
	require.NoError(t, db.QueryRow("SELECT column_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='messages' AND column_name='created_at'").Scan(&typ))
	require.Equal(t, "datetime(6)", typ)
	m, err := migrate.New("file://migrations/mysql", dsn)
	require.NoError(t, err)
	defer m.Close()
	require.NoError(t, m.Down())
	require.NoError(t, m.Up())
}

func TestMySQLDirtyNeverAutomaticallyForced(t *testing.T) {
	db, dsn := mysqlMigrationDB(t)
	chdirAndRestore(t, sqliteRepoRoot(t))
	require.NoError(t, RunMigrations(dsn))
	_, err := db.Exec("UPDATE schema_migrations SET dirty=true")
	require.NoError(t, err)
	err = RunMigrationsWithOptions(dsn, MigrationOptions{AutoRecoverDirty: true})
	require.Error(t, err)
	var dirty bool
	require.NoError(t, db.QueryRow("SELECT dirty FROM schema_migrations").Scan(&dirty))
	require.True(t, dirty)
	require.Contains(t, err.Error(), "dirty")
	require.NotContains(t, err.Error(), "Or enable AutoRecoverDirty")
}

func TestMySQLBaselineBusinessConstraints(t *testing.T) {
	db, dsn := mysqlMigrationDB(t)
	chdirAndRestore(t, sqliteRepoRoot(t))
	require.NoError(t, RunMigrations(dsn))

	for _, pair := range [][2]string{{"tenant_sandbox_configs", "cordoned_at"}, {"messages", "artifacts"}} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?", pair[0], pair[1]).Scan(&count))
		require.Equal(t, 1, count, "%s.%s", pair[0], pair[1])
	}
	var seed string
	require.NoError(t, db.QueryRow("SELECT JSON_EXTRACT(overlay, '$.providers') FROM model_catalog_configs WHERE id=1").Scan(&seed))
	require.Equal(t, "{}", seed)

	_, err := db.Exec("INSERT INTO mcp_endpoints (id, tenant_id, token_hash) VALUES ('endpoint-1', 1, 'token-A'), ('endpoint-2', 1, '')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO mcp_endpoints (id, tenant_id, token_hash) VALUES ('endpoint-3', 1, '')")
	require.NoError(t, err, "multiple empty tokens must be allowed")
	_, err = db.Exec("INSERT INTO mcp_endpoints (id, tenant_id, token_hash) VALUES ('endpoint-4', 1, 'token-A')")
	require.Error(t, err, "live nonempty token must be unique")
	_, err = db.Exec("INSERT INTO mcp_endpoints (id, tenant_id, token_hash) VALUES ('endpoint-5', 1, 'token-a')")
	require.NoError(t, err, "credential comparison must be case sensitive")
	_, err = db.Exec("UPDATE mcp_endpoints SET deleted_at=CURRENT_TIMESTAMP(6) WHERE id='endpoint-1'")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO mcp_endpoints (id, tenant_id, token_hash) VALUES ('endpoint-6', 1, 'token-A')")
	require.NoError(t, err, "soft-deleted token must not reserve a live key")

	principal := strings.Repeat("p", 511) + "A"
	name := strings.Repeat("n", 254) + "A"
	insertEnv := "INSERT INTO tenant_user_env_vars (id, tenant_id, principal_type, principal_id, sandbox_config_id, skill_id, name) VALUES (?, 1, 'im_user', ?, 'sandbox-config', 'skill', ?)"
	_, err = db.Exec(insertEnv, "env-1", principal, name)
	require.NoError(t, err)
	_, err = db.Exec(insertEnv, "env-2", principal, name)
	require.Error(t, err, "full long composite key must be unique")
	_, err = db.Exec(insertEnv, "env-3", strings.Repeat("p", 511)+"B", name)
	require.NoError(t, err, "distinct suffix beyond any prefix must remain distinct")
	_, err = db.Exec(insertEnv, "env-4", strings.Repeat("p", 511)+"a", name)
	require.NoError(t, err, "case-sensitive principal must remain distinct")
}
