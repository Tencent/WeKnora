package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// agentInboxRepository persists agent inbox events (table agent_inbox,
// migrations versioned/000115 + sqlite/000034). Plain SQL only — the
// schema is deliberately free of JSONB expressions so the same code
// serves PostgreSQL and SQLite.
type agentInboxRepository struct {
	db *gorm.DB
}

// NewAgentInboxRepository constructs the production inbox repository.
func NewAgentInboxRepository(db *gorm.DB) interfaces.AgentInboxRepository {
	return &agentInboxRepository{db: db}
}

// Insert persists one event row.
func (r *agentInboxRepository) Insert(ctx context.Context, evt *types.AgentInboxEvent) error {
	return r.db.WithContext(ctx).Create(evt).Error
}

// inboxListLimitMax is the hard ceiling per query regardless of caller input.
const inboxListLimitMax = 100

// applyInboxFilters narrows tx with the non-empty fields of f.
func applyInboxFilters(tx *gorm.DB, f types.InboxQueryFilters) *gorm.DB {
	tx = tx.Where("tenant_id = ?", f.TenantID)
	if len(f.KBIDs) > 0 {
		tx = tx.Where("kb_id IN ?", f.KBIDs)
	}
	if f.UserID != "" {
		tx = tx.Where("user_id = ?", f.UserID)
	}
	if len(f.Types) > 0 {
		tx = tx.Where("type IN ?", f.Types)
	}
	if f.UnreadOnly {
		tx = tx.Where("read_at IS NULL")
	}
	if f.Since != nil {
		tx = tx.Where("created_at >= ?", *f.Since)
	}
	return tx
}

// List returns matching events, newest first.
func (r *agentInboxRepository) List(
	ctx context.Context, f types.InboxQueryFilters,
) ([]*types.AgentInboxEvent, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > inboxListLimitMax {
		limit = inboxListLimitMax
	}
	var rows []*types.AgentInboxEvent
	err := applyInboxFilters(r.db.WithContext(ctx), f).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// MarkRead stamps read_at on the given rows (idempotent for already-read).
func (r *agentInboxRepository) MarkRead(
	ctx context.Context, tenantID uint64, ids []string, at time.Time,
) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.AgentInboxEvent{}).
		Where("tenant_id = ? AND id IN ? AND read_at IS NULL", tenantID, ids).
		Update("read_at", at).Error
}

// CountUnread counts unread events matching the filters.
func (r *agentInboxRepository) CountUnread(
	ctx context.Context, f types.InboxQueryFilters,
) (int64, error) {
	f.UnreadOnly = true
	var count int64
	err := applyInboxFilters(r.db.WithContext(ctx).Model(&types.AgentInboxEvent{}), f).
		Count(&count).Error
	return count, err
}

// DeleteOlderThan removes expired rows (retention sweeper). Returns the
// number of deleted rows.
func (r *agentInboxRepository) DeleteOlderThan(
	ctx context.Context, cutoff time.Time,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&types.AgentInboxEvent{})
	return res.RowsAffected, res.Error
}
