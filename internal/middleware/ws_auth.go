// Package middleware: authentication for browser WebSocket upgrades.
//
// Browsers cannot attach custom headers (Authorization / X-API-Key) to a
// WebSocket handshake. The sandbox terminal mints a short-lived ticket over
// a normal authenticated POST and presents it as the ticket query parameter;
// AttachAuthenticatedUser then installs the same auth session Auth would,
// given the user the handler already resolved from that ticket.
package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// AttachAuthenticatedUser installs the same auth session the Auth middleware
// would, given an already-resolved user and tenant. On success the request
// carries the resolved auth session and true is returned; on failure the
// response has already been written and false is returned.
//
// The tenant is taken from jwtTenantID unless the caller already set
// X-Tenant-ID, mirroring how Auth honours that header. Membership and role
// are still resolved from the database by authenticateJWTUser, so passing a
// user here grants nothing on its own.
func AttachAuthenticatedUser(
	c *gin.Context,
	tenantService interfaces.TenantService,
	memberService interfaces.TenantMemberService,
	cfg *config.Config,
	user *types.User,
	jwtTenantID uint64,
) bool {
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid or expired token"})
		c.Abort()
		return false
	}
	if jwtTenantID != 0 && strings.TrimSpace(c.GetHeader("X-Tenant-ID")) == "" {
		c.Request.Header.Set("X-Tenant-ID", strconv.FormatUint(jwtTenantID, 10))
	}
	return authenticateJWTUser(c, tenantService, memberService, cfg, user, jwtTenantID)
}
