package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// AuditLogQuery is the cursor + filter set for listing audit log
// entries. AfterID is the last id from the previous page (rows with
// id < AfterID are returned, newest first); 0 means "from the top".
// Limit is capped at 100 inside the repository regardless of caller
// input — keeps unbounded scans off the table.
type AuditLogQuery struct {
	AfterID     uint64
	Limit       int
	Action      types.AuditAction
	Outcome     types.AuditOutcome
	ActorUserID string
}

// AuditLogRepository is the storage primitive for the audit table.
// All writes are inserts (immutable rows); the only "update" surface
// is none — once written, an entry is permanent.
type AuditLogRepository interface {
	Create(ctx context.Context, entry *types.AuditLog) error
	List(ctx context.Context, tenantID uint64, q *AuditLogQuery) ([]*types.AuditLog, error)
	// CountSinceForDedup is the rate-limit primitive for LogDenied —
	// returns the count of matching rows in the trailing window so the
	// service can skip writing duplicates. Filter is
	// (tenant_id, actor_user_id, action, request_path, created_at >= since).
	CountSinceForDedup(
		ctx context.Context,
		tenantID uint64,
		actorUserID string,
		action types.AuditAction,
		requestPath string,
		since time.Time,
	) (int64, error)
}

// AuditLogService is the high-level audit API the rest of the codebase
// uses. It owns timestamp defaulting (Log) and rate-limit dedup
// (LogDenied) so callers don't have to think about either.
type AuditLogService interface {
	// Log writes a single audit entry. Callers fill TenantID + Action +
	// any per-event fields; the service fills CreatedAt if zero.
	Log(ctx context.Context, entry *types.AuditLog) error
	// LogDenied records a middleware-level reject decision. Subject to
	// 1-minute sliding-window dedup keyed by
	// (tenant_id, actor_user_id, action, request_path) so a probing
	// client cannot flood the table.
	LogDenied(
		ctx context.Context,
		c *gin.Context,
		tenantID uint64,
		actorUserID, actorRole string,
		requiredRole types.TenantRole,
	) error
	List(ctx context.Context, tenantID uint64, q *AuditLogQuery) ([]*types.AuditLog, error)
}
