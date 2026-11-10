package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Tenant 10 shared the KB (as user "sharer") into org-1; tenant 20 is an org
// admin; tenant 30 is a plain org editor.
type shareMgmtOrgRepo struct {
	interfaces.OrganizationRepository
}

func (shareMgmtOrgRepo) GetTenantMember(
	_ context.Context, _ string, tenantID uint64,
) (*types.OrganizationTenantMember, error) {
	switch tenantID {
	case 10:
		return &types.OrganizationTenantMember{TenantID: 10, Role: types.OrgRoleEditor}, nil
	case 20:
		return &types.OrganizationTenantMember{TenantID: 20, Role: types.OrgRoleAdmin}, nil
	case 30:
		return &types.OrganizationTenantMember{TenantID: 30, Role: types.OrgRoleEditor}, nil
	}
	return nil, repository.ErrOrgMemberNotFound
}

type shareMgmtKBShareRepo struct {
	interfaces.KBShareRepository
	share   *types.KnowledgeBaseShare
	updated bool
}

func (r *shareMgmtKBShareRepo) GetByID(context.Context, string) (*types.KnowledgeBaseShare, error) {
	copied := *r.share
	return &copied, nil
}

func (r *shareMgmtKBShareRepo) Update(context.Context, *types.KnowledgeBaseShare) error {
	r.updated = true
	return nil
}

func shareMgmtCtx(role types.TenantRole) context.Context {
	return context.WithValue(context.Background(), types.TenantRoleContextKey, role)
}

func TestCanManageShare(t *testing.T) {
	record := shareRecord{sharedByUserID: "sharer", sourceTenantID: 10, orgID: "org-1", permission: types.OrgRoleEditor}
	cases := []struct {
		name      string
		role      types.TenantRole
		userID    string
		tenantID  uint64
		requested types.OrgMemberRole
		want      bool
	}{
		{"sharer raises from source tenant", types.TenantRoleContributor, "sharer", 10, types.OrgRoleAdmin, true},
		{"sharer demoted to viewer", types.TenantRoleViewer, "sharer", 10, types.OrgRoleAdmin, false},
		{"sharer acting from another tenant", types.TenantRoleOwner, "sharer", 30, "", false},
		{"source tenant admin", types.TenantRoleAdmin, "someone", 10, types.OrgRoleAdmin, true},
		{"source tenant contributor", types.TenantRoleContributor, "someone", 10, "", false},
		{"org admin removes", types.TenantRoleAdmin, "org-admin", 20, "", true},
		{"org admin lowers", types.TenantRoleAdmin, "org-admin", 20, types.OrgRoleViewer, true},
		{"org admin keeps level", types.TenantRoleAdmin, "org-admin", 20, types.OrgRoleEditor, true},
		{"org admin raises", types.TenantRoleAdmin, "org-admin", 20, types.OrgRoleAdmin, false},
		{"viewer inside org-admin tenant", types.TenantRoleViewer, "viewer", 20, "", false},
		{"org editor tenant admin", types.TenantRoleAdmin, "editor-admin", 30, "", false},
		{"non-member", types.TenantRoleOwner, "outsider", 40, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := shareMgmtCtx(tc.role)
			got := canManageShare(ctx, shareMgmtOrgRepo{}, record, tc.requested, tc.userID, tc.tenantID)
			require.Equal(t, tc.want, got)
		})
	}
}

// An org admin that does not own the KB must not escalate a viewer share: that
// would give every editor member write access to the owner's KB.
func TestUpdateSharePermissionRejectsOrgAdminEscalation(t *testing.T) {
	repo := &shareMgmtKBShareRepo{share: &types.KnowledgeBaseShare{
		ID: "share-1", KnowledgeBaseID: "kb-1", OrganizationID: "org-1",
		SharedByUserID: "sharer", SourceTenantID: 10, Permission: types.OrgRoleViewer,
	}}
	svc := &kbShareService{shareRepo: repo, orgRepo: shareMgmtOrgRepo{}}

	ctx := shareMgmtCtx(types.TenantRoleAdmin)
	err := svc.UpdateSharePermission(ctx, "share-1", types.OrgRoleEditor, "org-admin", 20)
	require.ErrorIs(t, err, ErrSharePermissionDenied)
	require.False(t, repo.updated)
}

type revokeOrgRepo struct {
	shareMgmtOrgRepo
	removed uint64
}

func (*revokeOrgRepo) GetByID(context.Context, string) (*types.Organization, error) {
	return &types.Organization{ID: "org-1", OwnerTenantID: 99}, nil
}

func (r *revokeOrgRepo) RemoveTenantMember(_ context.Context, _ string, tenantID uint64) error {
	r.removed = tenantID
	return nil
}

type revokeKBShareRepo struct {
	interfaces.KBShareRepository
	revoked []uint64
}

func (r *revokeKBShareRepo) DeleteByOrganizationAndSourceTenant(_ context.Context, _ string, tenantID uint64) error {
	r.revoked = append(r.revoked, tenantID)
	return nil
}

type revokeAgentShareRepo struct {
	interfaces.AgentShareRepository
	revoked []uint64
}

func (r *revokeAgentShareRepo) DeleteByOrganizationAndSourceTenant(_ context.Context, _ string, tenantID uint64) error {
	r.revoked = append(r.revoked, tenantID)
	return nil
}

// A departing tenant's shares must not keep serving its KBs and agents to the
// remaining members, whether it left or was removed by an org admin.
func TestRemoveTenantMemberRevokesItsShares(t *testing.T) {
	for _, operator := range []uint64{10, 20} {
		orgRepo := &revokeOrgRepo{}
		kbShares := &revokeKBShareRepo{}
		agentShares := &revokeAgentShareRepo{}
		svc := &organizationService{orgRepo: orgRepo, shareRepo: kbShares, agentShareRepo: agentShares}

		require.NoError(t, svc.RemoveTenantMember(context.Background(), "org-1", 10, "user", operator))
		require.Equal(t, uint64(10), orgRepo.removed)
		require.Equal(t, []uint64{10}, kbShares.revoked)
		require.Equal(t, []uint64{10}, agentShares.revoked)
	}
}

type lapsedShareRepo struct {
	interfaces.KBShareRepository
	shares []*types.KnowledgeBaseShare
}

func (r *lapsedShareRepo) ListByKnowledgeBase(context.Context, string) ([]*types.KnowledgeBaseShare, error) {
	return r.shares, nil
}

// Shares left behind by a departed source tenant, or in a deleted
// organization, grant nothing.
func TestCheckTenantKBPermissionIgnoresLapsedShares(t *testing.T) {
	org := &types.Organization{ID: "org-1"}
	svc := &kbShareService{orgRepo: shareMgmtOrgRepo{}, shareRepo: &lapsedShareRepo{shares: []*types.KnowledgeBaseShare{
		{OrganizationID: "org-1", SourceTenantID: 50, Permission: types.OrgRoleAdmin, Organization: org},
		{OrganizationID: "org-1", SourceTenantID: 10, Permission: types.OrgRoleAdmin},
		{OrganizationID: "org-1", SourceTenantID: 10, Permission: types.OrgRoleViewer, Organization: org},
	}}}

	permission, shared, err := svc.CheckTenantKBPermission(context.Background(), "kb-1", 30, types.TenantRoleAdmin)
	require.NoError(t, err)
	require.True(t, shared)
	require.Equal(t, types.OrgRoleViewer, permission)
}
