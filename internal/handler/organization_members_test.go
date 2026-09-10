package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type organizationMemberServiceStub struct {
	interfaces.OrganizationService
	members []*types.OrganizationTenantMember
}

func (s *organizationMemberServiceStub) GetTenantMember(_ context.Context, _ string, tenantID uint64) (*types.OrganizationTenantMember, error) {
	return &types.OrganizationTenantMember{TenantID: tenantID}, nil
}

func (s *organizationMemberServiceStub) ListTenantMembers(context.Context, string) ([]*types.OrganizationTenantMember, error) {
	return s.members, nil
}

type organizationMemberTenantServiceStub struct {
	interfaces.TenantService
	tenants map[uint64]*types.Tenant
}

func (s *organizationMemberTenantServiceStub) GetTenantsByIDs(context.Context, []uint64) (map[uint64]*types.Tenant, error) {
	return s.tenants, nil
}

func TestListMembersReturnsAllTenantMemberships(t *testing.T) {
	gin.SetMode(gin.TestMode)
	joinedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	members := []*types.OrganizationTenantMember{
		{ID: "member-101", TenantID: 101, RepresentativeUserID: "user-1", Role: types.OrgRoleAdmin, CreatedAt: joinedAt},
		{ID: "member-102", TenantID: 102, RepresentativeUserID: "user-2", Role: types.OrgRoleEditor, CreatedAt: joinedAt},
		{ID: "member-103", TenantID: 103, RepresentativeUserID: "user-3", Role: types.OrgRoleViewer, CreatedAt: joinedAt},
	}
	handler := &OrganizationHandler{
		orgService: &organizationMemberServiceStub{members: members},
		tenantService: &organizationMemberTenantServiceStub{tenants: map[uint64]*types.Tenant{
			101: {ID: 101, Name: "Workspace A"},
			102: {ID: 102, Name: "Workspace B"},
			103: {ID: 103, Name: "Workspace C"},
		}},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/members", nil)
	c.Params = gin.Params{{Key: "id", Value: "org-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(101))

	handler.ListMembers(c)

	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool                      `json:"success"`
		Data    types.ListMembersResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, int64(3), response.Data.Total)
	require.Equal(t, []uint64{101, 102, 103}, []uint64{
		response.Data.Members[0].TenantID,
		response.Data.Members[1].TenantID,
		response.Data.Members[2].TenantID,
	})
	require.Equal(t, []string{"Workspace A", "Workspace B", "Workspace C"}, []string{
		response.Data.Members[0].TenantName,
		response.Data.Members[1].TenantName,
		response.Data.Members[2].TenantName,
	})
}
