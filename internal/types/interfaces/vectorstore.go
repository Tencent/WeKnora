package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// StoreRegistry provides VectorStore-based engine registration/lookup.
// Separated from RetrieveEngineRegistry to avoid changing the existing interface
// used by 6 services (17 call sites). Phase 2 may merge into RetrieveEngineRegistry.
type StoreRegistry interface {
	// RegisterWithStoreID registers an engine service by VectorStore ID.
	// Upsert semantics: existing entry is overwritten silently.
	RegisterWithStoreID(storeID string, svc RetrieveEngineService)
	// GetByStoreID retrieves an engine service by VectorStore ID.
	GetByStoreID(storeID string) (RetrieveEngineService, error)
	// UnregisterByStoreID removes an engine service by VectorStore ID (idempotent).
	UnregisterByStoreID(storeID string)
}

// EngineFactory creates a RetrieveEngineService from a VectorStore's config.
// Defined as a function type to avoid circular imports between container and service packages.
type EngineFactory func(ctx context.Context, store types.VectorStore) (RetrieveEngineService, error)

// VectorStoreService defines the service interface for vector store management.
// Tenant isolation is enforced by the handler layer (getOwnedStore pattern).
type VectorStoreService interface {
	// CreateStore validates and creates a new vector store.
	CreateStore(ctx context.Context, store *types.VectorStore) error
	// UpdateStore updates an existing vector store (name only).
	UpdateStore(ctx context.Context, store *types.VectorStore) error
	// DeleteStore deletes a vector store by tenant + id.
	// Rejects deletion when any active knowledge base is bound to the store
	// (binding guard); the caller must unbind or delete those KBs first.
	DeleteStore(ctx context.Context, tenantID uint64, id string) error
	// TestConnection tests connectivity to a vector database.
	// Returns the detected server version on success (e.g., "7.10.1"), empty string if unknown.
	TestConnection(ctx context.Context, engineType types.RetrieverEngineType, config types.ConnectionConfig) (string, error)
	// SaveDetectedVersion updates the connection_config.version for a stored vector store.
	SaveDetectedVersion(ctx context.Context, store *types.VectorStore, version string) error

	// ResolveStoreView returns the API-safe display projection of a single
	// store ID, scoped to the given tenant. Tries DB stores first, then the
	// cached env-store set. Returns types.UnavailableStoreDisplay() when the
	// store cannot be resolved — callers should still succeed in such cases
	// (the response carries a "unavailable" status signal for the UI).
	//
	// Never returns connection credentials in any form: the StoreDisplay
	// payload carries only Name / Source / EngineType / Status.
	ResolveStoreView(ctx context.Context, tenantID uint64, storeID string) (types.StoreDisplay, error)

	// BatchResolveStoreView resolves multiple store IDs in a single DB read
	// plus the cached env-store match. Returned map keys are the storeIDs
	// originally requested; missing IDs map to types.UnavailableStoreDisplay().
	//
	// Intended for list endpoints that need store metadata for many KBs at
	// once without incurring N+1 ResolveStoreView calls.
	BatchResolveStoreView(ctx context.Context, tenantID uint64, storeIDs []string) (map[string]types.StoreDisplay, error)
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
