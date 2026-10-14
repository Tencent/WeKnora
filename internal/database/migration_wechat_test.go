package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestWeChatSQLiteMigrationMatchesModels(t *testing.T) {
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	dbPath := filepath.Join(t.TempDir(), "wechat.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(
		t,
		db.Exec(
			`INSERT INTO users (id, username, email, password_hash)
VALUES ('user', 'wx_user', 'user@wechat.invalid', 'hash')`,
		).Error,
	)
	setting := types.WeChatAuthSetting{ID: 1, Version: 1, WebAppSecret: "enc:v1:test"}
	require.NoError(t, db.Create(&setting).Error)
	var loadedSetting types.WeChatAuthSetting
	require.NoError(t, db.First(&loadedSetting, 1).Error)
	require.Equal(t, setting.WebAppSecret, loadedSetting.WebAppSecret)
	identity := types.ExternalIdentity{Provider: "wechat_openid", Namespace: "web", Subject: "openid", UserID: "user"}
	require.NoError(t, db.Create(&identity).Error)
	require.Error(t, db.Create(&identity).Error, "identity tuples must be unique")
	identity.Namespace = "mobile"
	require.NoError(t, db.Create(&identity).Error, "OpenID is scoped to its application")
	a := types.WeChatLoginAttempt{
		ID:        "attempt",
		Client:    "web_result",
		AppID:     "web",
		Challenge: "challenge",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
		Result:    `{"token":"test"}`,
	}
	require.NoError(t, db.Create(&a).Error)
	var consumed types.WeChatLoginAttempt
	result := db.Clauses(clause.Returning{}).
		Where("id = ? AND expires_at > ?", "attempt", time.Now().UTC()).
		Delete(&consumed)
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
	require.Equal(t, a.Result, consumed.Result)

	m, err := migrate.New("file://migrations/sqlite", "sqlite3://"+dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))
	require.False(t, db.Migrator().HasTable(&types.ExternalIdentity{}))
	require.False(t, db.Migrator().HasTable(&types.WeChatLoginAttempt{}))
	var users int64
	require.NoError(t, db.Table("users").Count(&users).Error)
	require.EqualValues(t, 1, users)
	require.False(t, db.Migrator().HasTable(&types.WeChatAuthSetting{}))
	require.False(t, db.Migrator().HasColumn("users", "auth_source"))
	require.NoError(
		t,
		db.Exec(
			`INSERT INTO users (id, username, email, password_hash, preferences)
VALUES ('oidc', 'oidc_user', 'oidc@example.com', 'hash', '{"oidc_only_login":true}')`,
		).Error,
	)
	require.NoError(t, m.Steps(1))
	var sources []struct {
		ID         string
		AuthSource string
	}
	require.NoError(t, db.Table("users").Select("id, auth_source").Order("id").Find(&sources).Error)
	require.Equal(t, "oidc", sources[0].AuthSource)
	require.Equal(t, "local", sources[1].AuthSource)
	require.True(t, db.Migrator().HasTable(&types.WeChatAuthSetting{}))
	require.True(t, db.Migrator().HasTable(&types.ExternalIdentity{}))
	require.True(t, db.Migrator().HasTable(&types.WeChatLoginAttempt{}))
}
