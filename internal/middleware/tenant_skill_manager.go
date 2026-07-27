package middleware

import (
	"context"
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type TenantSkillMembershipLookup interface {
	Get(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error)
}

func RequireTenantSkillManager(members TenantSkillMembershipLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if _, isAPIKey := types.TenantAPIKeyScopeFromContext(ctx); isAPIKey {
			abortTenantSkillManager(c)
			return
		}
		userID, userOK := types.UserIDFromContext(ctx)
		tenantID, tenantOK := types.TenantIDFromContext(ctx)
		if !userOK || !tenantOK || members == nil {
			abortTenantSkillManager(c)
			return
		}
		member, err := members.Get(ctx, userID, tenantID)
		if err != nil || !isActiveTenantSkillManager(member, userID, tenantID) {
			abortTenantSkillManager(c)
			return
		}
		c.Next()
	}
}

func isActiveTenantSkillManager(member *types.TenantMember, userID string, tenantID uint64) bool {
	if member == nil || member.UserID != userID || member.TenantID != tenantID {
		return false
	}
	if member.Status != types.TenantMemberStatusActive {
		return false
	}
	return member.Role == types.TenantRoleOwner || member.Role == types.TenantRoleAdmin
}

func abortTenantSkillManager(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": "Forbidden: tenant owner or admin JWT required",
	})
}
