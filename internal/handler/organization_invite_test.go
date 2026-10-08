package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type inviteRepOrg struct {
	interfaces.OrganizationService
	members        []*types.OrganizationTenantMember
	representative *string
}

func (*inviteRepOrg) IsTenantOrgAdmin(context.Context, string, uint64) (bool, error) {
	return true, nil
}

// GetTenantMember reports a caller that is a member of the org when the
// roster is set, and a not-yet-member invite target otherwise.
func (o *inviteRepOrg) GetTenantMember(
	_ context.Context, _ string, tenantID uint64,
) (*types.OrganizationTenantMember, error) {
	for _, member := range o.members {
		if member.TenantID == tenantID {
			return member, nil
		}
	}
	return nil, repository.ErrOrgMemberNotFound
}

func (o *inviteRepOrg) AddTenantMember(_ context.Context, _ string, _ uint64, rep string, _ types.OrgMemberRole) error {
	o.representative = &rep
	return nil
}

func (o *inviteRepOrg) ListTenantMembers(context.Context, string) ([]*types.OrganizationTenantMember, error) {
	return o.members, nil
}

type inviteRepTenants struct{ interfaces.TenantService }

func (inviteRepTenants) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func (inviteRepTenants) GetTenantsByIDs(_ context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	out := map[uint64]*types.Tenant{}
	for _, id := range ids {
		out[id] = &types.Tenant{ID: id}
	}
	return out, nil
}

type inviteRepUsers struct{ interfaces.UserService }

func (inviteRepUsers) GetUserByID(_ context.Context, id string) (*types.User, error) {
	return &types.User{ID: id, TenantID: 42}, nil
}

func inviteRepContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "org"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	return c, recorder
}

// A direct add must not let the inviter attach a user of the other workspace
// as its representative: the roster would then show that user to members.
func TestInviteMemberAttachesNoRepresentative(t *testing.T) {
	for _, body := range []string{
		`{"tenant_id":42,"representative_user_id":"victim","role":"viewer"}`,
		`{"user_id":"victim","role":"viewer"}`,
	} {
		org := &inviteRepOrg{}
		h := &OrganizationHandler{orgService: org, tenantService: inviteRepTenants{}, userService: inviteRepUsers{}}
		c, _ := inviteRepContext(body)
		h.InviteMember(c)
		require.Empty(t, c.Errors, body)
		require.NotNil(t, org.representative, body)
		require.Empty(t, *org.representative, body)
	}
}

// Other workspaces' users' emails are not part of the roster.
func TestListMembersShowsEmailOnlyForOwnWorkspace(t *testing.T) {
	org := &inviteRepOrg{members: []*types.OrganizationTenantMember{
		{ID: "m-own", TenantID: 7, RepresentativeUser: &types.User{Username: "me", Email: "me@own.example"}},
		{ID: "m-other", TenantID: 42, RepresentativeUser: &types.User{Username: "them", Email: "them@other.example"}},
	}}
	h := &OrganizationHandler{orgService: org, tenantService: inviteRepTenants{}}
	c, recorder := inviteRepContext("")
	h.ListMembers(c)
	require.Empty(t, c.Errors)
	require.Contains(t, recorder.Body.String(), "me@own.example")
	require.NotContains(t, recorder.Body.String(), "them@other.example")
	require.Contains(t, recorder.Body.String(), `"username":"them"`)
}

type scopedListKBShares struct{ interfaces.KBShareService }

func (scopedListKBShares) ListSharedKnowledgeBases(
	context.Context, uint64, types.TenantRole,
) ([]*types.SharedKnowledgeBaseInfo, error) {
	return []*types.SharedKnowledgeBaseInfo{
		{KnowledgeBase: &types.KnowledgeBase{ID: "kb-allowed", Name: "Allowed KB"}},
		{KnowledgeBase: &types.KnowledgeBase{ID: "kb-other", Name: "Other KB"}},
	}, nil
}

// A KB-restricted API key must not learn about shared KBs outside its
// allow-list from the organization listings.
func TestSharedKnowledgeBaseListRespectsAPIKeyScope(t *testing.T) {
	h := &OrganizationHandler{shareService: scopedListKBShares{}}
	c, recorder := inviteRepContext("")
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityManageSpaces)},
	})
	c.Request = c.Request.WithContext(ctx)

	h.ListSharedKnowledgeBases(c)
	require.Empty(t, c.Errors)
	require.Contains(t, recorder.Body.String(), "Allowed KB")
	require.NotContains(t, recorder.Body.String(), "Other KB")
}
