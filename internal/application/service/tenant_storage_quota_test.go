package service

import (
	"context"
	"math"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func quotaTestSettings(values map[string]int64) *systemSettingService {
	s := &systemSettingService{cache: make(map[string]*types.SystemSetting)}
	for unit, value := range values {
		key := "tenant.default_storage_quota_" + unit
		s.cache[key] = &types.SystemSetting{
			Key: key, Value: types.JSON(strconv.FormatInt(value, 10)), LastModifiedBy: "admin",
		}
	}
	s.loaded.Store(true)
	return s
}

func TestResolveDefaultTenantStorageQuota(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]int64
		mb, gb   string
		want     int64
	}{
		{name: "default", want: 10 * quotaGiB},
		{name: "legacy env", gb: "2", want: 2 * quotaGiB},
		{name: "legacy DB overrides env", settings: map[string]int64{"gb": 3}, gb: "2", want: 3 * quotaGiB},
		{name: "MB env overrides GB", mb: "50", settings: map[string]int64{"gb": 3}, want: 50 * quotaMiB},
		{name: "MB DB overrides env", mb: "100", settings: map[string]int64{"mb": 50}, want: 50 * quotaMiB},
		{
			name: "explicit zero MB restores GB", mb: "50", gb: "2",
			settings: map[string]int64{"mb": 0}, want: 2 * quotaGiB,
		},
		{name: "negative MB restores GB", mb: "-1", gb: "2", want: 2 * quotaGiB},
		{name: "invalid MB env restores GB", mb: "invalid", gb: "2", want: 2 * quotaGiB},
		{name: "zero GB defaults", gb: "0", want: 10 * quotaGiB},
		{name: "negative GB defaults", gb: "-1", want: 10 * quotaGiB},
		{name: "invalid GB defaults", gb: "invalid", want: 10 * quotaGiB},
		{name: "overflow MB restores GB", mb: strconv.FormatInt(math.MaxInt64, 10), gb: "2", want: 2 * quotaGiB},
		{name: "overflow GB defaults", gb: strconv.FormatInt(math.MaxInt64, 10), want: 10 * quotaGiB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_MB", tt.mb)
			t.Setenv("WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_GB", tt.gb)
			got := ResolveDefaultTenantStorageQuota(context.Background(), quotaTestSettings(tt.settings))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTenantStorageQuotaPersistsAcrossCreationPaths(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Tenant{}))
	settings := quotaTestSettings(map[string]int64{"mb": 50, "gb": 2})
	repo := repository.NewTenantRepository(db)
	tenants := NewTenantService(repo, nil, settings)

	manual, err := tenants.CreateTenant(ctx, &types.Tenant{Name: "manual"})
	require.NoError(t, err)
	users := &userService{userRepo: &provisioningUserRepo{}, tenantService: tenants}
	registered, err := users.Register(ctx, &types.RegisterRequest{
		Username: "alice", Email: "alice@example.com", Password: "password123",
	})
	require.NoError(t, err)
	explicit, err := tenants.CreateTenant(ctx, &types.Tenant{Name: "explicit", StorageQuota: 75 * quotaMiB})
	require.NoError(t, err)
	negative, err := tenants.CreateTenant(ctx, &types.Tenant{Name: "negative", StorageQuota: -1})
	require.NoError(t, err)
	for _, id := range []uint64{manual.ID, registered.TenantID, negative.ID} {
		stored, readErr := repo.GetTenantByID(ctx, id)
		require.NoError(t, readErr)
		require.Equal(t, int64(50)*quotaMiB, stored.StorageQuota)
	}
	stored, err := repo.GetTenantByID(ctx, explicit.ID)
	require.NoError(t, err)
	require.Equal(t, int64(75)*quotaMiB, stored.StorageQuota)

	// A runtime setting change affects later creations, including registration,
	// without rewriting established tenants or relying on the GORM 10 GiB tag.
	settings.cache["tenant.default_storage_quota_mb"].Value = types.JSON(`100`)
	later, err := tenants.CreateTenant(ctx, &types.Tenant{Name: "later"})
	require.NoError(t, err)
	require.Equal(t, int64(100)*quotaMiB, later.StorageQuota)
	stored, err = repo.GetTenantByID(ctx, manual.ID)
	require.NoError(t, err)
	require.Equal(t, int64(50)*quotaMiB, stored.StorageQuota)
}
