package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Each PostgreSQL run owns a schema; never migrate or change existing tables.
func userStatusBackends(t *testing.T, fn func(*testing.T, *gorm.DB)) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *gorm.DB
			var err error
			cfg := &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true}
			if backend == "sqlite" {
				db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "users.db")+"?_busy_timeout=5000"), cfg)
			} else {
				dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL locking tests")
				}
				adminDB, openErr := gorm.Open(postgres.Open(dsn), cfg)
				require.NoError(t, openErr)
				adminSQL, openErr := adminDB.DB()
				require.NoError(t, openErr)
				schema := "user_status_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				require.NoError(t, adminDB.Exec("CREATE SCHEMA "+schema).Error)
				t.Cleanup(func() {
					require.NoError(t, adminDB.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
					require.NoError(t, adminSQL.Close())
				})
				separator := " "
				if strings.Contains(dsn, "://") {
					separator = "?"
					if strings.Contains(dsn, "?") {
						separator = "&"
					}
				}
				db, err = gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), cfg)
			}
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.AutoMigrate(&types.User{}, &types.AuthToken{}))
			fn(t, db)
		})
	}
}

func seedStatusUser(t *testing.T, db *gorm.DB, id string, admin, active bool) {
	t.Helper()
	user := &types.User{
		ID: id, Username: id, Email: id + "@example.com",
		PasswordHash: "hash", IsSystemAdmin: admin, IsActive: true,
	}
	require.NoError(t, NewUserRepository(db).CreateUser(context.Background(), user))
	if !active {
		require.NoError(t, db.Model(user).Update("is_active", false).Error)
	}
}

func TestSetUserActiveGuardsAndSessions(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewUserRepository(db)
		seedStatusUser(t, db, "admin", true, true)
		seedStatusUser(t, db, "disabled-admin", true, false)
		seedStatusUser(t, db, "user", false, true)
		for _, kind := range []string{"access_token", "refresh_token"} {
			require.NoError(t, db.Create(&types.AuthToken{
				ID: kind, UserID: "user", Token: kind, TokenType: kind,
				ExpiresAt: time.Now().Add(time.Hour),
			}).Error)
		}
		_, err := repo.SetUserActive(ctx, "missing", "admin", false)
		require.ErrorIs(t, err, ErrUserNotFound)
		_, err = repo.SetUserActive(ctx, "admin", "admin", false)
		require.ErrorIs(t, err, ErrCannotDisableSelf)
		_, err = repo.SetUserActive(ctx, "admin", "another-actor", false)
		require.ErrorIs(t, err, ErrCannotDisableLastAdmin)
		_, err = repo.RevokeSystemAdmin(ctx, "admin", "another-actor")
		require.ErrorIs(t, err, ErrLastSystemAdmin)

		_, err = repo.SetUserActive(ctx, "user", "admin", true)
		require.NoError(t, err)
		var revoked int64
		require.NoError(t, db.Model(&types.AuthToken{}).Where("is_revoked = ?", true).Count(&revoked).Error)
		require.Zero(t, revoked, "enabling an already active user must preserve sessions")

		user, err := repo.SetUserActive(ctx, "user", "admin", false)
		require.NoError(t, err)
		require.False(t, user.IsActive)
		require.NoError(t, db.Model(&types.AuthToken{}).Where("is_revoked = ?", true).Count(&revoked).Error)
		require.EqualValues(t, 2, revoked)
		user, err = repo.SetUserActive(ctx, "user", "admin", true)
		require.NoError(t, err)
		require.True(t, user.IsActive)
		require.Equal(t, "hash", user.PasswordHash)
		require.Zero(t, user.TenantID)
		require.NoError(t, db.Model(&types.AuthToken{}).Where("is_revoked = ?", true).Count(&revoked).Error)
		require.EqualValues(t, 2, revoked, "re-enabling must not restore old sessions")
	})
}

func TestSetUserActiveRollsBackWhenRevocationFails(t *testing.T) {
	userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
		seedStatusUser(t, db, "user", false, true)
		require.NoError(t, db.Migrator().DropTable(&types.AuthToken{}))
		repo := NewUserRepository(db)
		_, err := repo.SetUserActive(context.Background(), "user", "admin", false)
		require.Error(t, err)
		user, err := repo.GetUserByID(context.Background(), "user")
		require.NoError(t, err)
		require.True(t, user.IsActive, "failed revocation must roll back the status change")
	})
}

func TestUserUpdatePreservesAccountStatus(t *testing.T) {
	for _, scope := range []struct {
		name     string
		tenantID uint64
	}{
		{name: "tenantless"},
		{name: "with-tenant", tenantID: 1},
	} {
		t.Run(scope.name, func(t *testing.T) {
			for _, change := range []struct {
				name   string
				active bool
			}{
				{name: "disable"},
				{name: "enable", active: true},
			} {
				t.Run(change.name, func(t *testing.T) {
					userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
						ctx := context.Background()
						repo := NewUserRepository(db)
						seedStatusUser(t, db, "user", false, !change.active)
						if scope.tenantID != 0 {
							require.NoError(t, db.Model(&types.User{}).Where("id = ?", "user").
								UpdateColumn("tenant_id", scope.tenantID).Error)
						}

						// Pause an ordinary update after its read, then commit an admin
						// status change before the stale user object is written back.
						stale, err := repo.GetUserByID(ctx, "user")
						require.NoError(t, err)
						_, err = repo.SetUserActive(ctx, "user", "admin", change.active)
						require.NoError(t, err)
						instructions := "updated preference"
						stale.Preferences.BrowserSearchInstructions = &instructions
						stale.PasswordHash = "updated hash"
						require.NoError(t, repo.UpdateUser(ctx, stale))

						stored, err := repo.GetUserByID(ctx, "user")
						require.NoError(t, err)
						require.Equal(t, change.active, stored.IsActive,
							"ordinary updates must preserve account status")
						require.Equal(t, scope.tenantID, stored.TenantID)
						require.Equal(t, "updated hash", stored.PasswordHash)
						require.Equal(t, &instructions, stored.Preferences.BrowserSearchInstructions)
					})
				})
			}
		})
	}
}

func TestConcurrentAdminChangesKeepOneActiveAdmin(t *testing.T) {
	for _, mode := range []string{"disable-disable", "disable-revoke", "revoke-revoke"} {
		t.Run(mode, func(t *testing.T) {
			userStatusBackends(t, func(t *testing.T, db *gorm.DB) {
				seedStatusUser(t, db, "a", true, true)
				seedStatusUser(t, db, "b", true, true)
				for i := 0; i < 20; i++ {
					require.NoError(t, db.Model(&types.User{}).
						Where("id IN ?", []string{"a", "b"}).
						Updates(map[string]any{"is_active": true, "is_system_admin": true}).Error)
					start := make(chan struct{})
					results := make(chan error, 2)
					for n, id := range []string{"a", "b"} {
						go func(n int, id string) {
							<-start
							ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
							defer cancel()
							repo := NewUserRepository(db)
							actor := "a"
							if id == "a" {
								actor = "b"
							}
							var err error
							if mode == "revoke-revoke" || (mode == "disable-revoke" && n == 1) {
								_, err = repo.RevokeSystemAdmin(ctx, id, actor)
							} else {
								_, err = repo.SetUserActive(ctx, id, actor, false)
							}
							results <- err
						}(n, id)
					}
					close(start)
					successes := 0
					for n := 0; n < 2; n++ {
						err := <-results
						if err == nil {
							successes++
						} else {
							require.True(t,
								errors.Is(err, ErrCannotDisableLastAdmin) || errors.Is(err, ErrLastSystemAdmin),
								"unexpected error: %v", err)
						}
					}
					require.Equal(t, 1, successes)
					var count int64
					require.NoError(t, db.Model(&types.User{}).
						Where("is_system_admin = ? AND is_active = ?", true, true).Count(&count).Error)
					require.EqualValues(t, 1, count)
				}
			})
		})
	}
}
