package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestUpdateTenantAllowsBucketSharedByAnotherTenant(t *testing.T) {
	t.Setenv(storageAllowSharedBucketEnv, "")

	repo := &sharedBucketTenantRepo{
		tenants: []*types.Tenant{
			{
				ID: 1,
				StorageEngineConfig: &types.StorageEngineConfig{
					DefaultProvider: "oss",
					OSS: &types.OSSEngineConfig{
						BucketName: "zdgeo",
						PathPrefix: "knowledge/3/",
					},
				},
			},
		},
	}
	service := &tenantService{repo: repo}
	tenant := &types.Tenant{
		ID:   2,
		Name: "shared-bucket-tenant",
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "oss",
			OSS: &types.OSSEngineConfig{
				BucketName: "zdgeo",
				PathPrefix: "knowledge/3/",
			},
		},
	}

	updated, err := service.UpdateTenant(context.Background(), tenant)

	require.NoError(t, err)
	require.Same(t, tenant, updated)
	require.Same(t, tenant, repo.updated)
}

func TestUpdateTenantRejectsBucketSharedByAnotherTenantWhenDisabled(t *testing.T) {
	t.Setenv(storageAllowSharedBucketEnv, "false")

	repo := &sharedBucketTenantRepo{
		tenants: []*types.Tenant{
			{
				ID: 1,
				StorageEngineConfig: &types.StorageEngineConfig{
					DefaultProvider: "oss",
					OSS: &types.OSSEngineConfig{
						BucketName: "zdgeo",
						PathPrefix: "knowledge/1/",
					},
				},
			},
		},
	}
	service := &tenantService{repo: repo}
	tenant := &types.Tenant{
		ID:   2,
		Name: "duplicate-bucket-tenant",
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "oss",
			OSS: &types.OSSEngineConfig{
				BucketName: "zdgeo",
				PathPrefix: "knowledge/2/",
			},
		},
	}

	updated, err := service.UpdateTenant(context.Background(), tenant)

	require.Error(t, err)
	require.Nil(t, updated)
	require.Contains(t, err.Error(), "存储桶名称「zdgeo」已被其他空间使用")
	require.Nil(t, repo.updated)
}

type sharedBucketTenantRepo struct {
	tenants []*types.Tenant
	updated *types.Tenant
}

func (r *sharedBucketTenantRepo) CreateTenant(_ context.Context, tenant *types.Tenant) error {
	r.tenants = append(r.tenants, tenant)
	return nil
}

func (r *sharedBucketTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	for _, tenant := range r.tenants {
		if tenant.ID == id {
			return tenant, nil
		}
	}
	return nil, nil
}

func (r *sharedBucketTenantRepo) GetTenantsByIDs(
	_ context.Context,
	ids []uint64,
) (map[uint64]*types.Tenant, error) {
	result := make(map[uint64]*types.Tenant)
	for _, id := range ids {
		if tenant, _ := r.GetTenantByID(context.Background(), id); tenant != nil {
			result[id] = tenant
		}
	}
	return result, nil
}

func (r *sharedBucketTenantRepo) ListTenants(_ context.Context) ([]*types.Tenant, error) {
	return r.tenants, nil
}

func (r *sharedBucketTenantRepo) SearchTenants(
	_ context.Context,
	_ string,
	_ uint64,
	_, _ int,
) ([]*types.Tenant, int64, error) {
	return r.tenants, int64(len(r.tenants)), nil
}

func (r *sharedBucketTenantRepo) UpdateTenant(_ context.Context, tenant *types.Tenant) error {
	r.updated = tenant
	return nil
}

func (r *sharedBucketTenantRepo) DeleteTenant(_ context.Context, _ uint64) error {
	return nil
}

func (r *sharedBucketTenantRepo) AdjustStorageUsed(
	_ context.Context,
	_ uint64,
	_ int64,
) error {
	return nil
}

func (r *sharedBucketTenantRepo) BulkSetStorageQuota(
	_ context.Context,
	_ int64,
) (int64, error) {
	return 0, nil
}
