package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// VectorStoreService defines the service interface for vector store management.
// Tenant isolation is enforced by the handler layer (getOwnedStore pattern).
type VectorStoreService interface {
	// CreateStore validates and creates a new vector store.
	CreateStore(ctx context.Context, store *types.VectorStore) error
	// UpdateStore updates an existing vector store (name only).
	UpdateStore(ctx context.Context, store *types.VectorStore) error
	// DeleteStore deletes a vector store by tenant + id.
	DeleteStore(ctx context.Context, tenantID uint64, id string) error
	// TestConnection tests connectivity to a vector database.
	// Returns the detected server version on success (e.g., "7.10.1"), empty string if unknown.
	TestConnection(ctx context.Context, engineType types.RetrieverEngineType, config types.ConnectionConfig) (string, error)
	// SaveDetectedVersion updates the connection_config.version for a stored vector store.
	SaveDetectedVersion(ctx context.Context, store *types.VectorStore, version string) error
}

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
	// UpdateConnectionConfig updates only the connection_config column
	UpdateConnectionConfig(ctx context.Context, store *types.VectorStore) error
	// Delete soft-deletes a vector store
	Delete(ctx context.Context, tenantID uint64, id string) error
	// ExistsByEndpointAndIndex checks if a store with the same endpoint and index already exists
	ExistsByEndpointAndIndex(ctx context.Context, tenantID uint64, engineType types.RetrieverEngineType, endpoint string, indexName string) (bool, error)
}
