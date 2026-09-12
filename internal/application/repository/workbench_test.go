package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newWorkbenchAuthorizationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, query := range []string{
		"CREATE TABLE users (id text PRIMARY KEY, is_active boolean, deleted_at datetime)",
		"CREATE TABLE tenants (id integer PRIMARY KEY, status text, deleted_at datetime)",
		"CREATE TABLE tenant_members (user_id text, tenant_id integer, status text, deleted_at datetime)",
		"CREATE TABLE sessions (id text PRIMARY KEY, tenant_id integer, deleted_at datetime)",
		"CREATE TABLE im_channel_sessions (session_id text)",
		"INSERT INTO users VALUES ('alice', true, NULL)",
		"INSERT INTO tenants VALUES (7, 'active', NULL), (8, 'active', NULL)",
		"INSERT INTO tenant_members VALUES ('alice', 7, 'active', NULL)",
		"INSERT INTO sessions VALUES ('session', 7, NULL), ('foreign', 8, NULL)",
	} {
		require.NoError(t, db.Exec(query).Error)
	}
	return db
}

func TestWorkbenchAuthorizationRepositoryProjectionAndFreshness(t *testing.T) {
	db := newWorkbenchAuthorizationDB(t)
	repo := NewWorkbenchAuthorizationRepository(db)
	ctx := context.Background()
	var selected []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:projection", func(tx *gorm.DB) {
		selected = append([]string(nil), tx.Statement.Selects...)
	}))
	got, err := repo.GetAuthorization(ctx, 7, "alice")
	require.NoError(t, err)
	require.Equal(t, &types.WorkbenchAuthorization{
		UserID: "alice", UserActive: true, TenantID: 7, TenantStatus: "active",
		MemberStatus: types.TenantMemberStatusActive,
	}, got)
	require.Equal(t, []string{
		"u.id AS user_id, u.is_active AS user_active, t.id AS tenant_id, " +
			"t.status AS tenant_status, m.status AS member_status",
	}, selected)

	require.NoError(t, db.Exec("UPDATE tenant_members SET status = 'suspended'").Error)
	got, err = repo.GetAuthorization(ctx, 7, "alice")
	require.NoError(t, err)
	require.Equal(t, types.TenantMemberStatusSuspended, got.MemberStatus)
	require.NoError(t, db.Exec("UPDATE users SET is_active = false").Error)
	require.NoError(t, db.Exec("UPDATE tenants SET status = 'disabled' WHERE id = 7").Error)
	got, err = repo.GetAuthorization(ctx, 7, "alice")
	require.NoError(t, err)
	require.False(t, got.UserActive)
	require.Equal(t, "disabled", got.TenantStatus)
}

func TestWorkbenchAuthorizationRepositoryMissingAndSoftDeleted(t *testing.T) {
	for _, table := range []string{"users", "tenants", "tenant_members"} {
		t.Run(table, func(t *testing.T) {
			db := newWorkbenchAuthorizationDB(t)
			repo := NewWorkbenchAuthorizationRepository(db)
			ctx := context.Background()
			for _, identity := range []struct {
				tenant uint64
				user   string
			}{{7, "missing"}, {8, "alice"}, {0, "alice"}, {7, ""}} {
				got, err := repo.GetAuthorization(ctx, identity.tenant, identity.user)
				require.NoError(t, err)
				require.Nil(t, got)
			}
			require.NoError(t, db.Table(table).Where("1 = 1").Update("deleted_at", "2026-09-10").Error)
			got, err := repo.GetAuthorization(ctx, 7, "alice")
			require.NoError(t, err)
			require.Nil(t, got, "soft-deleted identity must not be returned")
		})
	}
}

func TestWorkbenchAuthorizationRepositoryIMScope(t *testing.T) {
	db := newWorkbenchAuthorizationDB(t)
	repo := NewWorkbenchAuthorizationRepository(db)
	ctx := context.Background()
	found, err := repo.HasIMSession(ctx, 7, "session")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, db.Exec(
		"INSERT INTO im_channel_sessions VALUES ('session'), ('session'), ('foreign'), ('missing')",
	).Error)
	found, err = repo.HasIMSession(ctx, 7, "session")
	require.NoError(t, err)
	require.True(t, found)
	for _, id := range []string{"foreign", "missing", ""} {
		found, err = repo.HasIMSession(ctx, 7, id)
		require.NoError(t, err)
		require.False(t, found)
	}
	require.NoError(t, db.Model(&types.Session{}).Where("id = ?", "session").
		UpdateColumn("deleted_at", "2026-09-10").Error)
	found, err = repo.HasIMSession(ctx, 7, "session")
	require.NoError(t, err)
	require.False(t, found)
}

func TestWorkbenchAuthorizationRepositoryPropagatesFailure(t *testing.T) {
	db := newWorkbenchAuthorizationDB(t)
	repo := NewWorkbenchAuthorizationRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repo.GetAuthorization(ctx, 7, "alice")
	require.ErrorIs(t, err, context.Canceled)
	_, err = repo.HasIMSession(ctx, 7, "session")
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, db.Exec("DROP TABLE users").Error)
	_, err = repo.GetAuthorization(context.Background(), 7, "alice")
	require.Error(t, err)
	require.NoError(t, db.Exec("DROP TABLE im_channel_sessions").Error)
	_, err = repo.HasIMSession(context.Background(), 7, "session")
	require.Error(t, err)
}
