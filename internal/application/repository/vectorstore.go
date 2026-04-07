package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// vectorStoreRepository implements the VectorStoreRepository interface
type vectorStoreRepository struct {
	db *gorm.DB
}

// NewVectorStoreRepository creates a new vector store repository
func NewVectorStoreRepository(db *gorm.DB) interfaces.VectorStoreRepository {
	return &vectorStoreRepository{db: db}
}

// Create creates a new vector store
func (r *vectorStoreRepository) Create(ctx context.Context, store *types.VectorStore) error {
	return r.db.WithContext(ctx).Create(store).Error
}

// GetByID retrieves a vector store by ID within a tenant scope.
// Returns (nil, nil) when the record is not found (not an error).
func (r *vectorStoreRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.VectorStore, error) {
	var store types.VectorStore
	if err := r.db.WithContext(ctx).Where(
		"id = ? AND tenant_id = ?", id, tenantID,
	).First(&store).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &store, nil
}

// List lists all vector stores for a tenant (newest first)
func (r *vectorStoreRepository) List(ctx context.Context, tenantID uint64) ([]*types.VectorStore, error) {
	var stores []*types.VectorStore
	if err := r.db.WithContext(ctx).Where(
		"tenant_id = ?", tenantID,
	).Order("created_at DESC").Find(&stores).Error; err != nil {
		return nil, err
	}
	return stores, nil
}

// Update updates a vector store (only mutable fields: name).
// engine_type, connection_config, index_config are immutable and excluded via Select.
// updated_at is handled by the DB trigger, so it is not included in Select.
func (r *vectorStoreRepository) Update(ctx context.Context, store *types.VectorStore) error {
	return r.db.WithContext(ctx).Model(&types.VectorStore{}).Where(
		"id = ? AND tenant_id = ?", store.ID, store.TenantID,
	).Select("name").Updates(store).Error
}

// Delete soft-deletes a vector store
func (r *vectorStoreRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where(
		"id = ? AND tenant_id = ?", id, tenantID,
	).Delete(&types.VectorStore{}).Error
}

// ExistsByEndpointAndIndex checks if a store with the same endpoint and index already exists.
// Comparison is done at the application level because JSONB field extraction syntax
// differs between PostgreSQL and SQLite, and the row count is small (a few per tenant).
func (r *vectorStoreRepository) ExistsByEndpointAndIndex(
	ctx context.Context,
	tenantID uint64,
	engineType types.RetrieverEngineType,
	endpoint string,
	indexName string,
) (bool, error) {
	var stores []*types.VectorStore
	if err := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND engine_type = ?", tenantID, string(engineType),
	).Find(&stores).Error; err != nil {
		return false, err
	}
	for _, s := range stores {
		if s.ConnectionConfig.GetEndpoint() == endpoint &&
			s.IndexConfig.GetIndexNameOrDefault(engineType) == indexName {
			return true, nil
		}
	}
	return false, nil
}
