package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// VectorStoreRepository defines the repository interface for VectorStore CRUD.
type VectorStoreRepository interface {
	// Create creates a new vector store
	Create(ctx context.Context, store *types.VectorStore) error
	// GetByID retrieves a vector store by ID within a tenant scope
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.VectorStore, error)
	// List lists all vector stores for a tenant
	List(ctx context.Context, tenantID uint64) ([]*types.VectorStore, error)
	// Update updates a vector store (only mutable fields: name)
	Update(ctx context.Context, store *types.VectorStore) error
	// Delete soft-deletes a vector store
	Delete(ctx context.Context, tenantID uint64, id string) error
	// ExistsByEndpointAndIndex checks if a store with the same endpoint and index already exists
	ExistsByEndpointAndIndex(ctx context.Context, tenantID uint64, engineType types.RetrieverEngineType, endpoint string, indexName string) (bool, error)
}
