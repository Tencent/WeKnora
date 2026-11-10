package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type storageBackendRepository struct{ db *gorm.DB }

func NewStorageBackendRepository(db *gorm.DB) interfaces.StorageBackendRepository {
	return &storageBackendRepository{db: db}
}

func (r *storageBackendRepository) Create(ctx context.Context, backend *types.StorageBackend) error {
	return r.db.WithContext(ctx).Create(backend).Error
}

func (r *storageBackendRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.StorageBackend, error) {
	var backend types.StorageBackend
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&backend).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &backend, nil
}

func (r *storageBackendRepository) List(ctx context.Context, tenantID uint64) ([]*types.StorageBackend, error) {
	var backends []*types.StorageBackend
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&backends).Error
	return backends, err
}

func (r *storageBackendRepository) Update(ctx context.Context, backend *types.StorageBackend) error {
	return r.db.WithContext(ctx).Model(&types.StorageBackend{}).
		Where("tenant_id = ? AND id = ?", backend.TenantID, backend.ID).
		Select("name", "config", "status", "updated_at").Updates(backend).Error
}

func (r *storageBackendRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.StorageBackend{}).Error
}

func (r *storageBackendRepository) FindLegacyAlias(ctx context.Context, tenantID uint64, provider string) (*types.StorageBackend, error) {
	var backend types.StorageBackend
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND provider = ? AND legacy_alias = ?", tenantID, provider, true).First(&backend).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &backend, nil
}
