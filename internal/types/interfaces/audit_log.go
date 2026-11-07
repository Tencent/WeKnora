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
	AfterID      uint64
	Limit        int
	Action       types.AuditAction
	Outcome      types.AuditOutcome
	ActorUserID  string
	ScopeType    string
	ScopeID      string
	UnscopedOnly bool
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
	// DeleteOlderThan removes audit rows whose created_at is strictly
	// before cutoff and returns the affected row count. It is the
	// retention primitive driven by the daily background sweep.
	// Implementations should delete in a single statement (no per-row
	// fetch) so the long-tail cost stays at "one DELETE per sweep".
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	// ListCardActionRows returns the newest agent.card_action_executed rows
	// for one (tenant_id, target_id), newest first, capped at the list limit.
	// Feeds the card-action one-shot dedup. The caller MUST build target_id
	// with the same truncation the write path uses or the lookup misses
	// every row.
	ListCardActionRows(ctx context.Context, tenantID uint64, targetID string) ([]*types.AuditLog, error)
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
	// Purge deletes rows whose created_at is strictly older than the
	// retention horizon. retentionDays <= 0 makes the call a no-op,
	// which keeps the daily sweep cheap when retention is disabled.
	// Returns rows deleted; transient repo errors propagate.
	Purge(ctx context.Context, retentionDays int) (int64, error)
	// CardActionAlreadyExecuted reports whether this button (targetID) was
	// already successfully executed for this message and action name. The
	// incoming messageID/targetID are ALREADY truncated to the write-path
	// widths. Lookup errors propagate: the caller degrades to proceed — the
	// exclusive live-run claim is the real double-run backstop, and audit is
	// best-effort by design.
	CardActionAlreadyExecuted(ctx context.Context, tenantID uint64, targetID, messageID, actionName string) (bool, error)
}
