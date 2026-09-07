package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrWebhookOutboxNotFound = errors.New("webhook outbox event not found")

type webhookOutboxRepository struct {
	db *gorm.DB
}

func NewWebhookOutboxRepository(db *gorm.DB) interfaces.WebhookOutboxRepository {
	return &webhookOutboxRepository{db: db}
}

func (r *webhookOutboxRepository) Insert(ctx context.Context, row *types.TenantWebhookOutbox) error {
	return r.db.WithContext(ctx).Create(row).Error
}

func (r *webhookOutboxRepository) GetByEventID(ctx context.Context, eventID string) (*types.TenantWebhookOutbox, error) {
	var row types.TenantWebhookOutbox
	err := r.db.WithContext(ctx).Where("event_id = ?", eventID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWebhookOutboxNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *webhookOutboxRepository) ListPending(ctx context.Context, limit int) ([]*types.TenantWebhookOutbox, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []*types.TenantWebhookOutbox
	err := r.db.WithContext(ctx).
		Where("status = ?", types.WebhookOutboxPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *webhookOutboxRepository) MarkProcessed(ctx context.Context, eventID string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&types.TenantWebhookOutbox{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"status":       types.WebhookOutboxProcessed,
			"processed_at": now,
			"last_error":   "",
		}).Error
}

func (r *webhookOutboxRepository) MarkFailed(ctx context.Context, eventID, lastError string) error {
	return r.db.WithContext(ctx).Model(&types.TenantWebhookOutbox{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"status":     types.WebhookOutboxFailed,
			"last_error": clipErr(lastError),
		}).Error
}

func (r *webhookOutboxRepository) BumpAttempts(ctx context.Context, eventID, lastError string) error {
	return r.db.WithContext(ctx).Model(&types.TenantWebhookOutbox{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"attempts":   gorm.Expr("attempts + 1"),
			"last_error": clipErr(lastError),
		}).Error
}

func (r *webhookOutboxRepository) DeleteProcessedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("status = ? AND processed_at IS NOT NULL AND processed_at < ?", types.WebhookOutboxProcessed, cutoff).
		Delete(&types.TenantWebhookOutbox{})
	return res.RowsAffected, res.Error
}

func clipErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 512 {
		return s[:512]
	}
	return s
}
