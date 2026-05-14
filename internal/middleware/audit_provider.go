package middleware

import (
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// auditServiceContextKey is the gin context key used by
// AuditServiceProvider to stash the running AuditLogService so that
// middleware functions (rbac.go's RequireRole / RequireOwnershipOrRole)
// can pull it out without needing the service threaded into their
// signatures. Same pattern as the langfuse gin middleware.
const auditServiceContextKey = "weknora.audit_service"

// AuditServiceProvider returns a gin middleware that injects the audit
// service into every request's gin.Context. Wiring is centralised in
// router.NewRouter so each request gets the same instance for the
// lifetime of the process; the middleware is a no-op when svc is nil
// (e.g. lite mode where audit isn't configured) so the rbac reject
// path degrades gracefully.
func AuditServiceProvider(svc interfaces.AuditLogService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc != nil {
			c.Set(auditServiceContextKey, svc)
		}
		c.Next()
	}
}

// AuditServiceFromContext fetches the audit service injected by
// AuditServiceProvider, or returns nil if no provider was wired
// upstream. Callers MUST nil-check before invoking — audit failure
// must never break the underlying business operation.
func AuditServiceFromContext(c *gin.Context) interfaces.AuditLogService {
	if v, ok := c.Get(auditServiceContextKey); ok {
		if svc, ok := v.(interfaces.AuditLogService); ok {
			return svc
		}
	}
	return nil
}
