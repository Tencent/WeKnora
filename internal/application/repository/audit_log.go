package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// auditLogRepository persists audit events to the audit_logs table.
// Rows are append-only — there is no Update, no soft-delete, no
// Modify hook. The cursor pagination uses the monotonic id column,
// not created_at, so duplicate timestamps don't break iteration.
type auditLogRepository struct {
	db *gorm.DB
}

// NewAuditLogRepository constructs the production audit log repository
// backed by the shared GORM connection.
func NewAuditLogRepository(db *gorm.DB) interfaces.AuditLogRepository {
	return &auditLogRepository{db: db}
}

// Create inserts a single audit row. Validation is light — Action is
// the only required field at the schema level; CreatedAt is filled by
// the database default if zero. Service-layer Log() fills both before
// calling here so this is mostly a pass-through.
func (r *auditLogRepository) Create(ctx context.Context, entry *types.AuditLog) error {
	return r.db.WithContext(ctx).Create(entry).Error
}

// auditLogListLimitMax is the hard ceiling regardless of caller input.
// Keeps a misconfigured client from triggering an unbounded scan; the
// frontend's default page size is 50 so this is comfortable headroom.
const auditLogListLimitMax = 100

// List returns audit log rows for a tenant, newest-first, applying the
// optional cursor and filters from AuditLogQuery. Both Action and
// Outcome are exact-match — the filter set is small enough that we
// don't need substring matching yet.
func (r *auditLogRepository) List(
	ctx context.Context,
	tenantID uint64,
	q *interfaces.AuditLogQuery,
) ([]*types.AuditLog, error) {
	limit := 50
	if q != nil && q.Limit > 0 {
		limit = q.Limit
	}
	if limit > auditLogListLimitMax {
		limit = auditLogListLimitMax
	}

	tx := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID)
	if q != nil {
		if q.AfterID > 0 {
			tx = tx.Where("id < ?", q.AfterID)
		}
		if q.Action != "" {
			tx = tx.Where("action = ?", q.Action)
		}
		if q.Outcome != "" {
			tx = tx.Where("outcome = ?", q.Outcome)
		}
		if q.ActorUserID != "" {
			tx = tx.Where("actor_user_id = ?", q.ActorUserID)
		}
	}

	var entries []*types.AuditLog
	if err := tx.Order("id DESC").Limit(limit).Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

// CountSinceForDedup powers AuditLogService.LogDenied's sliding-window
// dedup. The (tenant_id, action) index makes the count cheap; the
// remaining filters are applied as additional WHERE clauses on the
// indexed result set.
//
// Returning early on the first match would be slightly cheaper, but
// gorm's Count works on the same plan and the constant overhead is
// negligible compared to the dedup benefit.
func (r *auditLogRepository) CountSinceForDedup(
	ctx context.Context,
	tenantID uint64,
	actorUserID string,
	action types.AuditAction,
	requestPath string,
	since time.Time,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.AuditLog{}).
		Where("tenant_id = ?", tenantID).
		Where("actor_user_id = ?", actorUserID).
		Where("action = ?", action).
		Where("request_path = ?", requestPath).
		Where("created_at >= ?", since).
		Count(&count).Error
	return count, err
}

// DeleteOlderThan purges rows strictly older than cutoff in a single
// DELETE. The retention sweep (driven by the audit log service) calls
// it once a day with cutoff = now - retention_days.
//
// Tenant scope is intentionally not part of this signature: retention
// is a global ops policy, not a per-tenant choice. If we ever need
// per-tenant retention, we'd add a separate DeleteOlderThanForTenant
// rather than overload this primitive.
//
// Returns the number of rows affected so the caller can log the sweep
// outcome at INFO. Errors propagate verbatim — the caller decides
// whether they're terminal or transient.
func (r *auditLogRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&types.AuditLog{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
