package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Each PostgreSQL run owns a schema; never migrate or change existing tables.
func webSearchProviderBackends(t *testing.T, fn func(*testing.T, *gorm.DB)) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *gorm.DB
			var err error
			cfg := &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true}
			if backend == "sqlite" {
				db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "web_search_providers.db")+"?_busy_timeout=5000"), cfg)
			} else {
				dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL default-transition tests")
				}
				adminDB, openErr := gorm.Open(postgres.Open(dsn), cfg)
				require.NoError(t, openErr)
				adminSQL, openErr := adminDB.DB()
				require.NoError(t, openErr)
				schema := "web_search_provider_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
			require.NoError(t, db.AutoMigrate(&types.WebSearchProviderEntity{}))
			fn(t, db)
		})
	}
}

func makeWebSearchProvider(tenantID uint64, name string, isDefault bool) *types.WebSearchProviderEntity {
	return &types.WebSearchProviderEntity{
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		Name:      name,
		Provider:  types.WebSearchProviderTypeDuckDuckGo,
		IsDefault: isDefault,
	}
}

func countDefaultProviders(t *testing.T, db *gorm.DB, tenantID uint64) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&types.WebSearchProviderEntity{}).
		Where("tenant_id = ? AND is_default = ?", tenantID, true).
		Count(&count).Error)
	return count
}

// TestWebSearchProviderDefaultTransitionKeepsOneDefault covers the sequential
// POST/PUT/PATCH paths: after every successful write the workspace has exactly
// one default, and it is the provider that was just promoted.
func TestWebSearchProviderDefaultTransitionKeepsOneDefault(t *testing.T) {
	webSearchProviderBackends(t, func(t *testing.T, db *gorm.DB) {
		repo := NewWebSearchProviderRepository(db)
		ctx := context.Background()
		const tenantID = uint64(101)

		first := makeWebSearchProvider(tenantID, "first", false)
		require.NoError(t, repo.Create(ctx, first))
		assert.Equal(t, int64(0), countDefaultProviders(t, db, tenantID))

		// Create with is_default=true demotes nothing (no default yet).
		second := makeWebSearchProvider(tenantID, "second", true)
		require.NoError(t, repo.Create(ctx, second))
		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID))

		// A second default creation demotes the previous default.
		third := makeWebSearchProvider(tenantID, "third", true)
		require.NoError(t, repo.Create(ctx, third))
		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID))
		reloadedSecond, err := repo.GetByID(ctx, tenantID, second.ID)
		require.NoError(t, err)
		require.NotNil(t, reloadedSecond)
		assert.False(t, reloadedSecond.IsDefault, "previous default must be demoted")

		// Update promoting an existing provider demotes the current default.
		first.IsDefault = true
		require.NoError(t, repo.Update(ctx, first))
		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID))
		reloadedThird, err := repo.GetByID(ctx, tenantID, third.ID)
		require.NoError(t, err)
		require.NotNil(t, reloadedThird)
		assert.False(t, reloadedThird.IsDefault, "update must demote the previous default")

		// PatchSettings is_default=true follows the same rule.
		require.NoError(t, repo.PatchSettings(ctx, third, []string{"is_default"}))
		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID))
		reloadedFirst, err := repo.GetByID(ctx, tenantID, first.ID)
		require.NoError(t, err)
		require.NotNil(t, reloadedFirst)
		assert.False(t, reloadedFirst.IsDefault, "patch must demote the previous default")

		// Patching a provider that does not exist must not demote anyone.
		err = repo.PatchSettings(ctx, makeWebSearchProvider(tenantID, "ghost", true), []string{"is_default"})
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID))
	})
}

// TestWebSearchProviderCreateDefaultRollsBackOnFailure reproduces the
// failure boundary of #4453: when the insert of a new default provider fails,
// the previous default must survive. The baseline implementation cleared the
// default in a separate write, leaving the workspace with no default.
func TestWebSearchProviderCreateDefaultRollsBackOnFailure(t *testing.T) {
	webSearchProviderBackends(t, func(t *testing.T, db *gorm.DB) {
		repo := NewWebSearchProviderRepository(db)
		ctx := context.Background()
		const tenantID = uint64(102)

		existing := makeWebSearchProvider(tenantID, "existing", true)
		require.NoError(t, repo.Create(ctx, existing))
		require.Equal(t, int64(1), countDefaultProviders(t, db, tenantID))

		// Fail only the insert of the new default provider, mirroring the
		// BEFORE INSERT trigger injection from the issue report.
		if db.Dialector.Name() == "postgres" {
			require.NoError(t, db.Exec(
				`CREATE FUNCTION fail_boom_insert() RETURNS trigger AS $$
					BEGIN
						IF NEW.name = 'boom' THEN
							RAISE EXCEPTION 'injected insert failure';
						END IF;
						RETURN NEW;
					END;
				$$ LANGUAGE plpgsql`).Error)
			require.NoError(t, db.Exec(
				`CREATE TRIGGER fail_boom_insert BEFORE INSERT ON web_search_providers
					FOR EACH ROW EXECUTE FUNCTION fail_boom_insert()`).Error)
		} else {
			require.NoError(t, db.Exec(
				`CREATE TRIGGER fail_boom_insert BEFORE INSERT ON web_search_providers
					WHEN NEW.name = 'boom'
					BEGIN
						SELECT RAISE(ABORT, 'injected insert failure');
					END`).Error)
		}

		failing := makeWebSearchProvider(tenantID, "boom", true)
		require.Error(t, repo.Create(ctx, failing), "injected insert failure must surface")

		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID),
			"the previous default must survive a failed default creation")
		survivor, err := repo.GetDefault(ctx, tenantID)
		require.NoError(t, err)
		require.NotNil(t, survivor)
		assert.Equal(t, existing.ID, survivor.ID)
	})
}

// TestWebSearchProviderConcurrentDefaultCreates covers the concurrent POST
// path from #4453 on PostgreSQL: 32 concurrent default creations must leave
// exactly one default. On SQLite the file-level write lock serializes the
// transactions, so the concurrency check is PostgreSQL-only.
func TestWebSearchProviderConcurrentDefaultCreates(t *testing.T) {
	webSearchProviderBackends(t, func(t *testing.T, db *gorm.DB) {
		if db.Dialector.Name() != "postgres" {
			t.Skip("advisory-lock serialization is PostgreSQL-specific")
		}
		repo := NewWebSearchProviderRepository(db)
		ctx := context.Background()
		const tenantID = uint64(103)
		const concurrency = 32

		var wg sync.WaitGroup
		errs := make(chan error, concurrency)
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs <- repo.Create(ctx, makeWebSearchProvider(tenantID, fmt.Sprintf("racer-%d", i), true))
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}

		assert.Equal(t, int64(1), countDefaultProviders(t, db, tenantID),
			"concurrent default creations must leave exactly one default")
		def, err := repo.GetDefault(ctx, tenantID)
		require.NoError(t, err)
		require.NotNil(t, def)
	})
}
