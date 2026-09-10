package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListTenantMembersPreservesDistinctTenants(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.User{}, &types.OrganizationTenantMember{}))

	repo := NewOrganizationRepository(db)
	ctx := context.Background()
	const orgID = "org-1"

	for i, tenantID := range []uint64{101, 102, 103} {
		require.NoError(t, repo.AddTenantMember(ctx, &types.OrganizationTenantMember{
			ID:                   fmt.Sprintf("member-%d", tenantID),
			OrganizationID:       orgID,
			TenantID:             tenantID,
			Role:                 types.OrgRoleViewer,
			RepresentativeUserID: fmt.Sprintf("user-%d", i+1),
		}))
	}
	require.NoError(t, repo.AddTenantMember(ctx, &types.OrganizationTenantMember{
		ID:             "other-org-member",
		OrganizationID: "org-2",
		TenantID:       104,
		Role:           types.OrgRoleViewer,
	}))

	members, err := repo.ListTenantMembers(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, members, 3)
	require.Equal(t, []uint64{101, 102, 103}, []uint64{
		members[0].TenantID,
		members[1].TenantID,
		members[2].TenantID,
	})
}
