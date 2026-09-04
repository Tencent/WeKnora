package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrWebhookEndpointNotFound = errors.New("webhook endpoint not found")

type webhookEndpointRepository struct {
	db *gorm.DB
}

func NewWebhookEndpointRepository(db *gorm.DB) interfaces.WebhookEndpointRepository {
	return &webhookEndpointRepository{db: db}
}

func (r *webhookEndpointRepository) Create(ctx context.Context, ep *types.TenantWebhookEndpoint) error {
	return r.db.WithContext(ctx).Create(ep).Error
}

func (r *webhookEndpointRepository) Update(ctx context.Context, ep *types.TenantWebhookEndpoint) error {
	return r.db.WithContext(ctx).Save(ep).Error
}

func (r *webhookEndpointRepository) SoftDelete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.TenantWebhookEndpoint{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrWebhookEndpointNotFound
	}
	return nil
}

func (r *webhookEndpointRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.TenantWebhookEndpoint, error) {
	var ep types.TenantWebhookEndpoint
	err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&ep).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWebhookEndpointNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func (r *webhookEndpointRepository) GetByIDUnscoped(ctx context.Context, id string) (*types.TenantWebhookEndpoint, error) {
	var ep types.TenantWebhookEndpoint
	err := r.db.WithContext(ctx).Unscoped().Where("id = ?", id).First(&ep).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWebhookEndpointNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func (r *webhookEndpointRepository) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantWebhookEndpoint, error) {
	var rows []*types.TenantWebhookEndpoint
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&rows).Error
	return rows, err
}

func (r *webhookEndpointRepository) ListEnabledByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantWebhookEndpoint, error) {
	var rows []*types.TenantWebhookEndpoint
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND enabled = ?", tenantID, true).
		Order("created_at ASC").
		Find(&rows).Error
	return rows, err
}

func (r *webhookEndpointRepository) CountByTenant(ctx context.Context, tenantID uint64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.TenantWebhookEndpoint{}).Where("tenant_id = ?", tenantID).Count(&n).Error
	return n, err
}

func (r *webhookEndpointRepository) FindByTenantURL(ctx context.Context, tenantID uint64, url string) (*types.TenantWebhookEndpoint, error) {
	var ep types.TenantWebhookEndpoint
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND url = ?", tenantID, url).First(&ep).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ep, nil
}
