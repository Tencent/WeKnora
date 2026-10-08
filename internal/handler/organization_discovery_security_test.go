package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type inviteTestOrg struct{ interfaces.OrganizationService }

func (*inviteTestOrg) IsTenantOrgAdmin(context.Context, string, uint64) (bool, error) {
	return true, nil
}

func (*inviteTestOrg) ListTenantMembers(context.Context, string) ([]*types.OrganizationTenantMember, error) {
	return nil, nil
}

type inviteTestTenants struct {
	interfaces.TenantService
	queried []uint64
}

func (s *inviteTestTenants) GetTenantsByIDs(_ context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	s.queried = append(s.queried, ids...)
	return map[uint64]*types.Tenant{42: {ID: 42, Name: "Target workspace"}}, nil
}

func TestInviteCandidatesRequireExactID(t *testing.T) {
	tenants := &inviteTestTenants{}
	handler := &OrganizationHandler{orgService: &inviteTestOrg{}, tenantService: tenants}
	for _, query := range []string{"", "target", "%", "42"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "org"}}
		c.Request = httptest.NewRequest("GET", "/search", nil)
		q := c.Request.URL.Query()
		q.Set("q", query)
		c.Request.URL.RawQuery = q.Encode()
		handler.SearchTenantsForInvite(c)
		require.Empty(t, c.Errors)
		if query != "42" {
			require.Empty(t, tenants.queried)
			require.Contains(t, recorder.Body.String(), `"data":[]`)
		} else {
			require.Equal(t, []uint64{42}, tenants.queried)
			require.Contains(t, recorder.Body.String(), "Target workspace")
		}
	}
}
