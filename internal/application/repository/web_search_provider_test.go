package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const webSearchProviderTestTenantID uint64 = 42

func newWebSearchProviderTestRepository(
	t *testing.T,
) (*gorm.DB, interfaces.WebSearchProviderRepository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "web-search-providers.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", dbPath)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(16)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE web_search_providers (
		id TEXT PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		provider TEXT NOT NULL,
		description TEXT,
		parameters TEXT,
		is_default INTEGER DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec("INSERT INTO tenants (id) VALUES (?)", webSearchProviderTestTenantID).Error)
	return db, NewWebSearchProviderRepository(db)
}

func newDuckDuckGoProvider(name string, isDefault bool) *types.WebSearchProviderEntity {
	return &types.WebSearchProviderEntity{
		TenantID:  webSearchProviderTestTenantID,
		Name:      name,
		Provider:  types.WebSearchProviderTypeDuckDuckGo,
		IsDefault: isDefault,
	}
}

func requireDefaultProviderCount(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&types.WebSearchProviderEntity{}).
		Where("tenant_id = ? AND is_default = ?", webSearchProviderTestTenantID, true).
		Count(&count).Error)
	require.Equal(t, want, count)
}

func runConcurrentProviderWrites(t *testing.T, writes []func() error) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, len(writes))
	var wg sync.WaitGroup
	for _, write := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- write()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestWebSearchProviderRepositorySerializesConcurrentDefaultCreates(t *testing.T) {
	db, repo := newWebSearchProviderTestRepository(t)

	writes := make([]func() error, 12)
	for i := range writes {
		provider := newDuckDuckGoProvider(fmt.Sprintf("provider-%d", i), true)
		writes[i] = func() error {
			return repo.Create(context.Background(), provider)
		}
	}
	runConcurrentProviderWrites(t, writes)

	requireDefaultProviderCount(t, db, 1)
	var total int64
	require.NoError(t, db.Model(&types.WebSearchProviderEntity{}).Count(&total).Error)
	require.Equal(t, int64(len(writes)), total)
}

func TestWebSearchProviderRepositorySerializesConcurrentDefaultUpdates(t *testing.T) {
	db, repo := newWebSearchProviderTestRepository(t)

	providers := make([]*types.WebSearchProviderEntity, 12)
	for i := range providers {
		providers[i] = newDuckDuckGoProvider(fmt.Sprintf("provider-%d", i), false)
		require.NoError(t, repo.Create(context.Background(), providers[i]))
	}
	writes := make([]func() error, len(providers))
	for i, provider := range providers {
		provider.IsDefault = true
		writes[i] = func() error {
			return repo.Update(context.Background(), provider)
		}
	}
	runConcurrentProviderWrites(t, writes)

	requireDefaultProviderCount(t, db, 1)
}

func TestWebSearchProviderRepositorySerializesConcurrentDefaultPatches(t *testing.T) {
	db, repo := newWebSearchProviderTestRepository(t)

	providers := make([]*types.WebSearchProviderEntity, 12)
	for i := range providers {
		providers[i] = newDuckDuckGoProvider(fmt.Sprintf("provider-%d", i), false)
		require.NoError(t, repo.Create(context.Background(), providers[i]))
	}
	writes := make([]func() error, len(providers))
	for i, provider := range providers {
		provider.IsDefault = true
		writes[i] = func() error {
			return repo.PatchSettings(context.Background(), provider, []string{"is_default"})
		}
	}
	runConcurrentProviderWrites(t, writes)

	requireDefaultProviderCount(t, db, 1)
}

func TestWebSearchProviderRepositoryRollsBackFailedDefaultWrite(t *testing.T) {
	db, repo := newWebSearchProviderTestRepository(t)
	ctx := context.Background()
	baseline := newDuckDuckGoProvider("baseline", true)
	require.NoError(t, repo.Create(ctx, baseline))

	missing := newDuckDuckGoProvider("missing", true)
	missing.ID = "missing"
	err := repo.Update(ctx, missing)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	requireDefaultProviderCount(t, db, 1)
	current, err := repo.GetDefault(ctx, webSearchProviderTestTenantID)
	require.NoError(t, err)
	require.Equal(t, baseline.ID, current.ID)
}

func TestWebSearchProviderRepositoryStopsWhenDefaultScopeCannotBeLocked(t *testing.T) {
	db, repo := newWebSearchProviderTestRepository(t)
	provider := newDuckDuckGoProvider("orphan", true)
	provider.TenantID++

	err := repo.Create(context.Background(), provider)
	require.True(t, errors.Is(err, gorm.ErrRecordNotFound), err)

	var count int64
	require.NoError(t, db.Model(&types.WebSearchProviderEntity{}).
		Where("tenant_id = ?", provider.TenantID).Count(&count).Error)
	require.Zero(t, count)
}
