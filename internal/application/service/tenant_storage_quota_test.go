package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// quotaTestTenantRepo is an in-memory TenantRepository for the
// UpdateStorageQuota tests. Only the two methods the service path
// touches are implemented; everything else panics via the embedded nil
// interface, which surfaces test bugs instead of silently passing.
type quotaTestTenantRepo struct {
	interfaces.TenantRepository
	tenant      *types.Tenant
	updateCalls int
}

func (r *quotaTestTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if r.tenant != nil && r.tenant.ID == id {
		// Copy so the service's in-place mutation only becomes visible
		// through UpdateTenant, mirroring real DB semantics.
		cp := *r.tenant
		return &cp, nil
	}
	return nil, errors.New("tenant not found")
}

func (r *quotaTestTenantRepo) UpdateTenant(_ context.Context, tenant *types.Tenant) error {
	r.updateCalls++
	cp := *tenant
	r.tenant = &cp
	return nil
}

// stubLocalStorageFree swaps the disk probe for the duration of a test.
func stubLocalStorageFree(t *testing.T, free int64, err error) {
	t.Helper()
	orig := localStorageFreeBytesFn
	localStorageFreeBytesFn = func() (int64, error) { return free, err }
	t.Cleanup(func() { localStorageFreeBytesFn = orig })
}

func TestUpdateStorageQuota(t *testing.T) {
	const (
		tenantID    = uint64(1)
		currentQuot = int64(1000)
		storageUsed = int64(400)
		diskFree    = int64(600)
	)
	probeErr := errors.New("statfs failed")

	tests := []struct {
		name string
		// quota the Owner asks for
		quota int64
		// disk probe stub; probeErr simulates an unreadable volume
		free     int64
		probeErr error
		// wantErr asserts the change is rejected and nothing is persisted
		wantErr bool
	}{
		{name: "zero quota rejected", quota: 0, free: diskFree, wantErr: true},
		{name: "negative quota rejected", quota: -5, free: diskFree, wantErr: true},
		{name: "quota below current usage rejected", quota: storageUsed - 1, free: diskFree, wantErr: true},
		{name: "quota above disk headroom rejected", quota: diskFree + storageUsed + 1, free: diskFree, wantErr: true},
		{name: "quota exactly at disk headroom allowed", quota: diskFree + storageUsed, free: diskFree},
		{name: "lowering quota to current usage allowed", quota: storageUsed, free: diskFree},
		{name: "raising rejected when disk probe fails", quota: currentQuot + 1, probeErr: probeErr, wantErr: true},
		{name: "lowering allowed when disk probe fails", quota: currentQuot - 200, probeErr: probeErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubLocalStorageFree(t, tt.free, tt.probeErr)
			repo := &quotaTestTenantRepo{tenant: &types.Tenant{
				ID:           tenantID,
				Name:         "workspace",
				StorageQuota: currentQuot,
				StorageUsed:  storageUsed,
			}}
			svc := NewTenantService(repo, nil)

			updated, err := svc.UpdateStorageQuota(context.Background(), tenantID, tt.quota)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("UpdateStorageQuota(%d) succeeded, want error", tt.quota)
				}
				if repo.updateCalls != 0 {
					t.Fatalf("UpdateTenant persisted %d times on a rejected change", repo.updateCalls)
				}
				if repo.tenant.StorageQuota != currentQuot {
					t.Fatalf("quota mutated to %d despite rejection", repo.tenant.StorageQuota)
				}
				return
			}
			if err != nil {
				t.Fatalf("UpdateStorageQuota(%d) failed: %v", tt.quota, err)
			}
			if updated.StorageQuota != tt.quota {
				t.Fatalf("returned quota=%d, want %d", updated.StorageQuota, tt.quota)
			}
			if repo.tenant.StorageQuota != tt.quota {
				t.Fatalf("persisted quota=%d, want %d", repo.tenant.StorageQuota, tt.quota)
			}
		})
	}
}

func TestGetTenantStorageStats(t *testing.T) {
	repo := &quotaTestTenantRepo{tenant: &types.Tenant{
		ID:           1,
		Name:         "workspace",
		StorageQuota: 1000,
		StorageUsed:  400,
	}}
	svc := NewTenantService(repo, nil)

	t.Run("probe success reports headroom", func(t *testing.T) {
		stubLocalStorageFree(t, 600, nil)
		stats, err := svc.GetTenantStorageStats(context.Background(), 1)
		if err != nil {
			t.Fatalf("GetTenantStorageStats failed: %v", err)
		}
		if stats.DiskUnavailable {
			t.Fatal("DiskUnavailable=true on a successful probe")
		}
		if stats.DiskFreeBytes != 600 {
			t.Fatalf("DiskFreeBytes=%d, want 600", stats.DiskFreeBytes)
		}
		if stats.QuotaMaxBytes != 1000 {
			t.Fatalf("QuotaMaxBytes=%d, want 1000 (600 free + 400 used)", stats.QuotaMaxBytes)
		}
		if stats.StorageUsedBytes != 400 {
			t.Fatalf("StorageUsedBytes=%d, want 400", stats.StorageUsedBytes)
		}
	})

	t.Run("probe failure degrades to usage-only ceiling", func(t *testing.T) {
		stubLocalStorageFree(t, 0, errors.New("statfs failed"))
		stats, err := svc.GetTenantStorageStats(context.Background(), 1)
		if err != nil {
			t.Fatalf("GetTenantStorageStats failed: %v", err)
		}
		if !stats.DiskUnavailable {
			t.Fatal("DiskUnavailable=false on a failed probe")
		}
		if stats.QuotaMaxBytes != 400 {
			t.Fatalf("QuotaMaxBytes=%d, want 400 (usage only)", stats.QuotaMaxBytes)
		}
	})
}
