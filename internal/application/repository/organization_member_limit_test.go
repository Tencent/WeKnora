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

// The member limit is enforced with the insert, not by a separate count the
// caller did earlier, so concurrent joins cannot exceed it.
func TestAddTenantMemberEnforcesLimitWithTheInsert(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE organization_tenant_members (
		id TEXT PRIMARY KEY, organization_id TEXT, tenant_id INTEGER, role TEXT,
		representative_user_id TEXT, joined_at DATETIME, created_at DATETIME, updated_at DATETIME
	)`).Error)
	repo := &organizationRepository{db: db}
	add := func(tenantID uint64, limit int) error {
		return repo.AddTenantMember(context.Background(), &types.OrganizationTenantMember{
			ID: fmt.Sprintf("m-%d", tenantID), OrganizationID: "org", TenantID: tenantID, Role: types.OrgRoleViewer,
		}, limit)
	}

	require.NoError(t, add(1, 2))
	require.NoError(t, add(2, 2))
	require.ErrorIs(t, add(3, 2), ErrOrgMemberLimitReached)
	require.ErrorIs(t, add(2, 0), ErrOrgMemberAlreadyExists)
	require.NoError(t, add(3, 0), "no limit")

	var count int64
	require.NoError(t, db.Model(&types.OrganizationTenantMember{}).Count(&count).Error)
	require.Equal(t, int64(3), count)
}
