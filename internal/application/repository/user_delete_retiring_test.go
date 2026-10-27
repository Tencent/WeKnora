package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// openRetireDB is the allocation SQLite pool plus a users table, so account
// deletion can lock the owner row. Allocation tests that never delete a web
// user keep using newAllocationRepo and do not create users.
func openRetireDB(t *testing.T) (*gorm.DB, *sandboxAllocationRepository, *userRepository, *sessionRepository) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&types.Session{}, &types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}, &types.Message{},
	))
	migrateUsers(t, db)
	return db,
		NewSandboxAllocationRepository(db).(*sandboxAllocationRepository),
		NewUserRepository(db).(*userRepository),
		NewSessionRepository(db).(*sessionRepository)
}

// migrateUsers creates the columns soft-delete and the owner lock need.
// AutoMigrate of types.User can fail on SQLite when the jsonb default is
// rejected; the fallback is the same NOT NULL columns the model requires.
func migrateUsers(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&types.User{}); err == nil {
		return
	}
	require.NoError(t, db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id varchar(36) PRIMARY KEY,
			username varchar(100) NOT NULL,
			email varchar(255) NOT NULL,
			password_hash varchar(255) NOT NULL,
			avatar varchar(500),
			tenant_id integer,
			is_active numeric,
			can_access_all_tenants numeric,
			is_system_admin numeric,
			preferences text NOT NULL DEFAULT '{}',
			created_at datetime,
			updated_at datetime,
			deleted_at datetime
		)
	`).Error)
}

func TestDeleteUserRetiringSessionsRetiresASessionInsertedUnderTheLock(t *testing.T) {
	db, alloc, users, sessions := openRetireDB(t)
	ctx := context.Background()
	owner := uuid.New().String()
	require.NoError(t, db.Create(&types.User{
		ID: owner, Username: "alice", Email: "alice@example.com", PasswordHash: "x",
	}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "s-1", TenantID: 7, UserID: owner,
	}).Error)

	afterWebUserLock = func(tx *gorm.DB) {
		require.NoError(t, tx.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
			ID: "s-2", TenantID: 7, UserID: owner,
		}).Error)
	}
	t.Cleanup(func() { afterWebUserLock = nil })

	err := users.DeleteUserRetiringSessions(ctx, owner, time.Now().UTC())
	afterWebUserLock = nil
	require.NoError(t, err)

	for _, id := range []string{"s-1", "s-2"} {
		row, err := alloc.GetAssignment(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, row, id)
		require.NotNil(t, row.ReleasedAt, id)
	}
	var gone types.User
	require.ErrorIs(t, db.Where("id = ?", owner).Take(&gone).Error, gorm.ErrRecordNotFound)
	require.NoError(t, db.Unscoped().Where("id = ?", owner).Take(&gone).Error)
	require.True(t, gone.DeletedAt.Valid)

	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "s-3", TenantID: 7, UserID: owner,
	}).Error)
	_, err = claim(ctx, alloc, userAssignment("s-3", "d-1"), interfaces.PlacementPin{})
	require.ErrorIs(t, err, interfaces.ErrSessionRetired)
	placed, err := alloc.GetAssignment(ctx, "s-3")
	require.NoError(t, err)
	require.Nil(t, placed, "a deleted owner never receives an assignment")

	before := countSessions(t, db, owner)
	_, err = sessions.Create(ctx, &types.Session{TenantID: 7, UserID: owner, Title: "late"})
	require.ErrorIs(t, err, errWebUserDeleted)
	require.Equal(t, before, countSessions(t, db, owner))
}

func TestSessionCreateAllowsANonCanonicalOwnerWithNoUsersRow(t *testing.T) {
	// No users table at all: a non-UUID owner must not query it.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", uuid.NewString())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	sessions := NewSessionRepository(db)

	created, err := sessions.Create(context.Background(), &types.Session{
		TenantID: 7, UserID: "u-1", Title: "plain",
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, int64(1), countSessions(t, db, "u-1"))
}

func countSessions(t *testing.T, db *gorm.DB, userID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&types.Session{}).Where("user_id = ?", userID).Count(&n).Error)
	return n
}
