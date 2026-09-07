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

var ErrWebhookDeliveryNotFound = errors.New("webhook delivery not found")

type webhookDeliveryRepository struct {
	db *gorm.DB
}

func NewWebhookDeliveryRepository(db *gorm.DB) interfaces.WebhookDeliveryRepository {
	return &webhookDeliveryRepository{db: db}
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint")
}

func (r *webhookDeliveryRepository) Claim(
	ctx context.Context,
	row *types.TenantWebhookDelivery,
) (bool, *types.TenantWebhookDelivery, error) {
	err := r.db.WithContext(ctx).Create(row).Error
	if err == nil {
		return true, row, nil
	}
	if !isUniqueViolation(err) {
		return false, nil, err
	}
	existing, getErr := r.GetByEventEndpoint(ctx, row.EventID, row.EndpointID)
	if getErr != nil {
		return false, nil, getErr
	}
	return false, existing, nil
}

func (r *webhookDeliveryRepository) GetByEventEndpoint(
	ctx context.Context,
	eventID, endpointID string,
) (*types.TenantWebhookDelivery, error) {
	var row types.TenantWebhookDelivery
	err := r.db.WithContext(ctx).Where("event_id = ? AND endpoint_id = ?", eventID, endpointID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *webhookDeliveryRepository) UpdateAttempt(
	ctx context.Context,
	deliveryID string,
	httpStatus, attempts, durationMs int,
	errMsg, status string,
	finished bool,
) error {
	updates := map[string]any{
		"http_status": httpStatus,
		"attempts":    attempts,
		"error":       clipErr(errMsg),
		"duration_ms": durationMs,
		"status":      status,
	}
	if finished {
		now := time.Now().UTC()
		updates["finished_at"] = now
	}
	return r.db.WithContext(ctx).Model(&types.TenantWebhookDelivery{}).
		Where("delivery_id = ?", deliveryID).
		Updates(updates).Error
}

func (r *webhookDeliveryRepository) ListByEndpoint(
	ctx context.Context,
	endpointID string,
	limit int,
) ([]*types.TenantWebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []*types.TenantWebhookDelivery
	err := r.db.WithContext(ctx).
		Where("endpoint_id = ?", endpointID).
		Order("id DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *webhookDeliveryRepository) ListEndpointIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.TenantWebhookDelivery{}).
		Distinct("endpoint_id").
		Pluck("endpoint_id", &ids).Error
	return ids, err
}

func (r *webhookDeliveryRepository) PruneEndpointKeepLatest(ctx context.Context, endpointID string, keep int) (int64, error) {
	if keep <= 0 {
		keep = types.WebhookDeliveryKeepPerEndpoint
	}
	var keepIDs []int64
	err := r.db.WithContext(ctx).Model(&types.TenantWebhookDelivery{}).
		Where("endpoint_id = ?", endpointID).
		Order("id DESC").
		Limit(keep).
		Pluck("id", &keepIDs).Error
	if err != nil {
		return 0, err
	}
	q := r.db.WithContext(ctx).Where("endpoint_id = ?", endpointID)
	if len(keepIDs) > 0 {
		q = q.Where("id NOT IN ?", keepIDs)
	}
	res := q.Delete(&types.TenantWebhookDelivery{})
	return res.RowsAffected, res.Error
}

func (r *webhookDeliveryRepository) DeleteOlderThanDays(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	res := r.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&types.TenantWebhookDelivery{})
	return res.RowsAffected, res.Error
}
