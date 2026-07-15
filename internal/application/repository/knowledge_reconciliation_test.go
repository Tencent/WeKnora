package repository

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const tenantsStorageTestDDL = `
CREATE TABLE IF NOT EXISTS tenants (
    id INTEGER PRIMARY KEY,
    storage_quota BIGINT NOT NULL DEFAULT 0,
    storage_used BIGINT NOT NULL DEFAULT 0
);
`

func setupKnowledgeReconciliationDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgesTestDDL).Error)
	require.NoError(t, db.Exec(tenantsStorageTestDDL).Error)
	return db
}

func seedKnowledgeStorage(t *testing.T, db *gorm.DB, storageSize, storageUsed, storageQuota int64) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(
		`INSERT INTO tenants (id, storage_quota, storage_used) VALUES (1, ?, ?)`,
		storageQuota,
		storageUsed,
	).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id, tenant_id, knowledge_base_id, type, title, source, parse_status, storage_size
		) VALUES (?, 1, ?, 'document', 'storage-test', 'file', 'processing', ?)
	`, id, uuid.New().String(), storageSize).Error)
	return id
}

func TestUpdateKnowledgeAndTenantStorageRollsBackAndRetriesIdempotently(t *testing.T) {
	db := setupKnowledgeReconciliationDB(t, "file:"+uuid.New().String()+"?mode=memory&cache=shared")
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	id := seedKnowledgeStorage(t, db, 100, 500, 1_000)

	knowledge, err := repo.GetKnowledgeByID(context.Background(), 1, id)
	require.NoError(t, err)
	knowledge.StorageSize = 200
	knowledge.Title = "updated"

	adjustErr := errors.New("injected tenant adjustment failure")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(
		"test:fail_tenant_storage_adjustment",
		func(tx *gorm.DB) {
			if tx.Statement.Table == "tenants" {
				tx.AddError(adjustErr)
			}
		},
	))

	_, err = repo.UpdateKnowledgeAndTenantStorage(context.Background(), knowledge)
	require.ErrorIs(t, err, adjustErr)

	var persistedStorage, tenantUsed int64
	require.NoError(t, db.Raw("SELECT storage_size FROM knowledges WHERE id = ?", id).Scan(&persistedStorage).Error)
	require.NoError(t, db.Raw("SELECT storage_used FROM tenants WHERE id = 1").Scan(&tenantUsed).Error)
	assert.EqualValues(t, 100, persistedStorage)
	assert.EqualValues(t, 500, tenantUsed)

	require.NoError(t, db.Callback().Update().Remove("test:fail_tenant_storage_adjustment"))
	delta, err := repo.UpdateKnowledgeAndTenantStorage(context.Background(), knowledge)
	require.NoError(t, err)
	assert.EqualValues(t, 100, delta)

	delta, err = repo.UpdateKnowledgeAndTenantStorage(context.Background(), knowledge)
	require.NoError(t, err)
	assert.Zero(t, delta)

	require.NoError(t, db.Raw("SELECT storage_size FROM knowledges WHERE id = ?", id).Scan(&persistedStorage).Error)
	require.NoError(t, db.Raw("SELECT storage_used FROM tenants WHERE id = 1").Scan(&tenantUsed).Error)
	assert.EqualValues(t, 200, persistedStorage)
	assert.EqualValues(t, 600, tenantUsed)
}

func TestWithKnowledgeReconciliationSerializesOverlappingAttempts(t *testing.T) {
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "reconciliation.db"))
	dsn := "file:" + dbPath + "?_busy_timeout=5000&_journal_mode=WAL"
	db1 := setupKnowledgeReconciliationDB(t, dsn)
	db2 := setupKnowledgeReconciliationDB(t, dsn)
	for _, db := range []*gorm.DB{db1, db2} {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	}

	id := seedKnowledgeStorage(t, db1, 100, 500, 1_000)
	repo1 := NewKnowledgeRepository(db1).(*knowledgeRepository)
	repo2 := NewKnowledgeRepository(db2).(*knowledgeRepository)

	var resource atomic.Value
	resource.Store("old")
	firstPassedCheck := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- repo1.WithKnowledgeReconciliation(
			context.Background(), 1, id,
			func(context.Context) error {
				close(firstPassedCheck)
				<-releaseFirst
				resource.Store("deleted-by-first")
				return nil
			},
		)
	}()
	<-firstPassedCheck

	go func() {
		secondDone <- repo2.WithKnowledgeReconciliation(
			context.Background(), 1, id,
			func(context.Context) error {
				close(secondEntered)
				resource.Store("newer-attempt")
				return nil
			},
		)
	}()

	select {
	case <-secondEntered:
		t.Fatal("newer attempt entered while the first attempt held the knowledge reconciliation lock")
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, "newer-attempt", resource.Load())
}

func TestWithKnowledgeReconciliationSharesTransactionWithCallbackWrites(t *testing.T) {
	db := setupKnowledgeReconciliationDB(t, "file:"+uuid.New().String()+"?mode=memory&cache=shared")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	id := seedKnowledgeStorage(t, db, 100, 500, 1_000)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	knowledge, err := repo.GetKnowledgeByID(context.Background(), 1, id)
	require.NoError(t, err)
	knowledge.Title = "written-under-reconciliation-lock"

	require.NoError(t, repo.WithKnowledgeReconciliation(
		context.Background(), 1, id,
		func(ctx context.Context) error {
			return repo.UpdateKnowledge(ctx, knowledge)
		},
	))

	persisted, err := repo.GetKnowledgeByID(context.Background(), 1, id)
	require.NoError(t, err)
	assert.Equal(t, "written-under-reconciliation-lock", persisted.Title)
}
