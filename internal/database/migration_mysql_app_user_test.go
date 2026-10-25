//go:build mysqlintegration

package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// The administrator only provisions a disposable database and account. Migrations
// must run with the same database-scoped privileges as the Compose application.
// The dedicated test server must use log_bin_trust_function_creators=1, as Compose does.
func TestMySQLMigrationsAsApplicationUser(t *testing.T) {
	admin, adminDSN := mysqlMigrationDB(t)
	var binlog bool
	require.NoError(t, admin.QueryRow("SELECT @@global.log_bin").Scan(&binlog))
	require.True(t, binlog, "exercise trigger permissions with binary logging enabled")
	chdirAndRestore(t, sqliteRepoRoot(t))
	cfg, err := mysql.ParseDSN(strings.TrimPrefix(adminDSN, "mysql://"))
	require.NoError(t, err)
	user := fmt.Sprintf("wk_test_%d", time.Now().UnixNano())
	password := "migration-test-password"
	_, err = admin.Exec("CREATE USER '" + user + "'@'%' IDENTIFIED BY '" + password + "'")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP USER '" + user + "'@'%'")
		require.NoError(t, err)
	})
	_, err = admin.Exec("GRANT ALL PRIVILEGES ON `" + cfg.DBName + "`.* TO '" + user + "'@'%'")
	require.NoError(t, err)
	cfg.User, cfg.Passwd = user, password
	app, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	var super string
	require.NoError(t, admin.QueryRow("SELECT Super_priv FROM mysql.user WHERE User=? AND Host='%'", user).Scan(&super))
	require.Equal(t, "N", super)
	cfg.User, cfg.Passwd = url.QueryEscape(user), url.QueryEscape(password)
	dsn := "mysql://" + cfg.FormatDSN()
	require.NoError(t, RunMigrations(dsn))
	require.NoError(t, RunMigrations(dsn), "repeat startup must be safe")
	var triggers int
	require.NoError(t, app.QueryRow("SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE()").Scan(&triggers))
	require.Equal(t, 3, triggers)
	var dirty bool
	require.NoError(t, app.QueryRow("SELECT dirty FROM schema_migrations").Scan(&dirty))
	require.False(t, dirty)
}
