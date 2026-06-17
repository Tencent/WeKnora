package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeUserTenantService struct {
	tenants map[uint64]*types.Tenant
}

func (f *fakeUserTenantService) CreateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeUserTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if tenant, ok := f.tenants[id]; ok {
		return tenant, nil
	}
	return nil, errors.New("tenant not found")
}

func (f *fakeUserTenantService) GetTenantsByIDs(_ context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	out := make(map[uint64]*types.Tenant, len(ids))
	for _, id := range ids {
		if tenant, ok := f.tenants[id]; ok {
			out[id] = tenant
		}
	}
	return out, nil
}

func (f *fakeUserTenantService) ListTenants(context.Context) ([]*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeUserTenantService) UpdateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeUserTenantService) DeleteTenant(context.Context, uint64) error {
	return errors.New("not implemented")
}

func (f *fakeUserTenantService) UpdateAPIKey(context.Context, uint64) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeUserTenantService) ExtractTenantIDFromAPIKey(string) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeUserTenantService) ListAllTenants(context.Context) ([]*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeUserTenantService) BulkSetStorageQuota(context.Context, int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeUserTenantService) SearchTenants(context.Context, string, uint64, int, int) ([]*types.Tenant, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (f *fakeUserTenantService) GetTenantByIDForUser(context.Context, uint64, string) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeUserTenantService) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

func TestBuildLoginMembershipsSkipsDeletedTenantRows(t *testing.T) {
	memberRepo := newFakeRepo()
	memberSvc := NewTenantMemberService(memberRepo, nil)
	now := time.Now()
	require.NoError(t, memberRepo.Create(context.Background(), &types.TenantMember{
		UserID:    "user-1",
		TenantID:  10000,
		Role:      types.TenantRoleOwner,
		Status:    types.TenantMemberStatusActive,
		JoinedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, memberRepo.Create(context.Background(), &types.TenantMember{
		UserID:    "user-1",
		TenantID:  10001,
		Role:      types.TenantRoleOwner,
		Status:    types.TenantMemberStatusActive,
		JoinedAt:  now.Add(time.Second),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	activeTenant := &types.Tenant{ID: 10000, Name: "active workspace"}
	svc := &userService{
		tenantService: &fakeUserTenantService{
			tenants: map[uint64]*types.Tenant{
				activeTenant.ID: activeTenant,
			},
		},
		memberService: memberSvc,
	}

	memberships := svc.BuildLoginMemberships(context.Background(), &types.User{
		ID:       "user-1",
		TenantID: activeTenant.ID,
	}, activeTenant)

	require.Len(t, memberships, 1)
	assert.Equal(t, uint64(10000), memberships[0].TenantID)
	assert.Equal(t, "active workspace", memberships[0].TenantName)
}
