//go:build mysqlintegration

// Package testutil provides disposable databases for MySQL integration tests.
package testutil

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	driver "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MySQL creates and migrates a fresh database for one test. The configured DSN
// must name a test database; cleanup never drops the configured database itself.
func MySQL(t testing.TB) *gorm.DB {
	t.Helper()
	raw := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	require.NotEmpty(t, raw, "mysqlintegration requires WEKNORA_MYSQL_TEST_DSN")
	cfg, err := driver.ParseDSN(strings.TrimPrefix(raw, "mysql://"))
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(cfg.DBName), "test", "use a disposable test database")
	adminCfg := cfg.Clone()
	adminCfg.DBName = ""
	admin, err := sql.Open("mysql", adminCfg.FormatDSN())
	require.NoError(t, err)
	name := "weknora_mysql_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE `" + name + "`")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	cfg.DBName = name
	cfg.ParseTime = true
	cfg.ClientFoundRows = true
	cfg.MultiStatements = false
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	migrationCfg := cfg.Clone()
	migrationCfg.User = url.QueryEscape(cfg.User)
	migrationCfg.Passwd = url.QueryEscape(cfg.Passwd)
	migrationCfg.MultiStatements = true
	root, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err = os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		require.NotEqual(t, root, parent, "cannot find repository root")
		root = parent
	}
	src, err := iofs.New(os.DirFS(root), "migrations/mysql")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", src, "mysql://"+migrationCfg.FormatDSN())
	require.NoError(t, err)
	require.NoError(t, m.Up())
	sourceErr, dbErr := m.Close()
	require.NoError(t, sourceErr)
	require.NoError(t, dbErr)
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	t.Log(fmt.Sprintf("MySQL integration database %s", name))
	return db
}
