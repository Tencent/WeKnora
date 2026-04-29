package retriever

import (
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// RetrieveEngineRegistry implements the retrieval engine registry.
// It maintains two maps:
//   - byEngineType: env stores registered via RETRIEVE_DRIVER (backward compatible)
//   - byStoreID: DB stores registered via VectorStore table (instance-based)
//
// Implements both interfaces.RetrieveEngineRegistry and interfaces.StoreRegistry.
type RetrieveEngineRegistry struct {
	byEngineType map[types.RetrieverEngineType]interfaces.RetrieveEngineService
	byStoreID    map[string]interfaces.RetrieveEngineService
	mu           sync.RWMutex
}

// NewRetrieveEngineRegistry creates a new retrieval engine registry
func NewRetrieveEngineRegistry() interfaces.RetrieveEngineRegistry {
	return &RetrieveEngineRegistry{
		byEngineType: make(map[types.RetrieverEngineType]interfaces.RetrieveEngineService),
		byStoreID:    make(map[string]interfaces.RetrieveEngineService),
	}
}

// --- interfaces.RetrieveEngineRegistry methods (unchanged behavior) ---

// Register registers a retrieval engine service by engine type.
// Returns an error if the engine type is already registered.
func (r *RetrieveEngineRegistry) Register(repo interfaces.RetrieveEngineService) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byEngineType[repo.EngineType()]; exists {
		return fmt.Errorf("repository type %s already registered", repo.EngineType())
	}

	r.byEngineType[repo.EngineType()] = repo
	return nil
}

// GetRetrieveEngineService retrieves a retrieval engine service by type.
// Only searches the byEngineType map (env stores).
func (r *RetrieveEngineRegistry) GetRetrieveEngineService(repoType types.RetrieverEngineType) (
	interfaces.RetrieveEngineService, error,
) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	repo, exists := r.byEngineType[repoType]
	if !exists {
		return nil, fmt.Errorf("repository of type %s not found", repoType)
	}

	return repo, nil
}

// GetAllRetrieveEngineServices retrieves all registered retrieval engine services.
// Only returns byEngineType entries (env stores) for backward compatibility.
func (r *RetrieveEngineRegistry) GetAllRetrieveEngineServices() []interfaces.RetrieveEngineService {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]interfaces.RetrieveEngineService, 0, len(r.byEngineType))
	for _, v := range r.byEngineType {
		result = append(result, v)
	}

	return result
}

// --- interfaces.StoreRegistry methods (new, for VectorStore-based engines) ---

// RegisterWithStoreID registers an engine service by VectorStore ID.
// Unlike Register(), the same EngineType can be registered multiple times
// with different StoreIDs (e.g., two Elasticsearch clusters).
// Upsert semantics: existing entry is overwritten silently.
func (r *RetrieveEngineRegistry) RegisterWithStoreID(storeID string, svc interfaces.RetrieveEngineService) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.byStoreID[storeID] = svc
}

// GetByStoreID retrieves an engine service by VectorStore ID.
// Callers must verify tenant ownership before using the returned service.
func (r *RetrieveEngineRegistry) GetByStoreID(storeID string) (interfaces.RetrieveEngineService, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	svc, exists := r.byStoreID[storeID]
	if !exists {
		return nil, fmt.Errorf("store %s not found in registry", storeID)
	}
	return svc, nil
}

// UnregisterByStoreID removes an engine service from the byStoreID map.
// Idempotent: returns silently if the storeID is not found.
//
// NOTE: gRPC-based clients (Qdrant, Milvus) hold connections that are not closed here.
// Known Phase 1 limitation — store deletion is rare, connections cleaned up on process exit.
// Phase 2 should add Close() to RetrieveEngineService interface and call it here.
func (r *RetrieveEngineRegistry) UnregisterByStoreID(storeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.byStoreID, storeID)
}
