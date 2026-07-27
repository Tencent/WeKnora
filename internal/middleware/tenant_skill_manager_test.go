package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type tenantSkillMembershipLookupStub struct {
	members map[string]*types.TenantMember
}

func (s *tenantSkillMembershipLookupStub) Get(
	_ context.Context,
	userID string,
	tenantID uint64,
) (*types.TenantMember, error) {
	return s.members[userID], nil
}

func TestRequireTenantSkillManagerPermissionMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		member     *types.TenantMember
		apiKey     bool
		missingJWT bool
		want       int
	}{
		{"owner", activeSkillMember("owner", types.TenantRoleOwner), false, false, http.StatusNoContent},
		{"admin", activeSkillMember("admin", types.TenantRoleAdmin), false, false, http.StatusNoContent},
		{"viewer", activeSkillMember("viewer", types.TenantRoleViewer), false, false, http.StatusForbidden},
		{"suspended owner", suspendedSkillMember("suspended", types.TenantRoleOwner), false, false, http.StatusForbidden},
		{"full api key", nil, true, false, http.StatusForbidden},
		{"missing jwt", nil, false, true, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := &tenantSkillMembershipLookupStub{members: map[string]*types.TenantMember{}}
			if tc.member != nil {
				lookup.members[tc.member.UserID] = tc.member
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				ctx := c.Request.Context()
				if !tc.missingJWT {
					ctx = context.WithValue(ctx, types.UserIDContextKey, memberUserID(tc.member))
					ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(10000))
				}
				if tc.apiKey {
					ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{FullAccess: true})
				}
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			router.POST("/skills/upload", RequireTenantSkillManager(lookup), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, "/skills/upload", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.want, response.Code)
		})
	}
}

func activeSkillMember(userID string, role types.TenantRole) *types.TenantMember {
	return &types.TenantMember{UserID: userID, TenantID: 10000, Role: role, Status: types.TenantMemberStatusActive}
}

func suspendedSkillMember(userID string, role types.TenantRole) *types.TenantMember {
	return &types.TenantMember{UserID: userID, TenantID: 10000, Role: role, Status: types.TenantMemberStatusSuspended}
}

func memberUserID(member *types.TenantMember) string {
	if member == nil {
		return "system-10000"
	}
	return member.UserID
}
