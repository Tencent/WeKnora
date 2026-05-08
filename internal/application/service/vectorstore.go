package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// vectorStoreService implements interfaces.VectorStoreService
type vectorStoreService struct {
	repo          interfaces.VectorStoreRepository
	storeRegistry interfaces.StoreRegistry // for dynamic registry updates on CRUD
	factory       interfaces.EngineFactory // creates engine services from VectorStore config
}

// NewVectorStoreService creates a new vector store service
func NewVectorStoreService(
	repo interfaces.VectorStoreRepository,
	storeRegistry interfaces.StoreRegistry,
	factory interfaces.EngineFactory,
) interfaces.VectorStoreService {
	return &vectorStoreService{
		repo:          repo,
		storeRegistry: storeRegistry,
		factory:       factory,
	}
}

// CreateStore validates and creates a new vector store.
func (s *vectorStoreService) CreateStore(ctx context.Context, store *types.VectorStore) error {
	// 1. Basic validation (name, engine_type, tenant_id)
	if err := store.Validate(); err != nil {
		return err
	}

	// 2. Engine-specific connection config validation
	if err := validateConnectionConfig(store.EngineType, store.ConnectionConfig); err != nil {
		return err
	}

	// 2.5. Index config validation (bounds, name characters)
	if err := types.ValidateIndexConfig(store.IndexConfig); err != nil {
		return err
	}

	// 3. Duplicate check — DB stores
	endpoint := store.ConnectionConfig.GetEndpoint()
	indexName := store.IndexConfig.GetIndexNameOrDefault(store.EngineType)

	exists, err := s.repo.ExistsByEndpointAndIndex(ctx, store.TenantID, store.EngineType, endpoint, indexName)
	if err != nil {
		return errors.NewInternalServerError("failed to check for duplicates")
	}
	if exists {
		return errors.NewConflictError("a vector store with the same endpoint and index already exists")
	}

	// 4. Duplicate check — env stores (pure function, no os.Getenv in types)
	for _, envStore := range types.BuildEnvVectorStores(os.Getenv("RETRIEVE_DRIVER"), os.Getenv) {
		if envStore.EngineType == store.EngineType &&
			envStore.ConnectionConfig.GetEndpoint() == endpoint &&
			envStore.IndexConfig.GetIndexNameOrDefault(store.EngineType) == indexName {
			return errors.NewConflictError(
				"a vector store with the same endpoint and index is already configured via environment variables")
		}
	}

	// 5. Auto-detect server version via connection test.
	// This is required for engines where the version determines the SDK (e.g., ES v7 vs v8).
	// Without it, the wrong SDK may be used causing protocol errors (406, etc.).
	version, err := s.TestConnection(ctx, store.EngineType, store.ConnectionConfig)
	if err != nil {
		return errors.NewBadRequestError(
			fmt.Sprintf("connection test failed: %s. Ensure the server is reachable before saving.", err.Error()))
	}
	if version != "" {
		store.ConnectionConfig.Version = version
	}

	// 6. Persist
	logger.Infof(ctx, "Creating vector store: tenant=%d, name=%s, engine=%s",
		store.TenantID, secutils.SanitizeForLog(store.Name), store.EngineType)
	if err := s.repo.Create(ctx, store); err != nil {
		return err
	}

	// 6. Register in registry (best-effort; failure doesn't roll back DB).
	// The store is already persisted, and will be loaded on next app restart (self-healing).
	s.registerInRegistry(ctx, store)

	return nil
}

// UpdateStore updates an existing vector store (name only).
// NOTE: If connection_config or index_config become mutable in the future,
// registry re-registration must be added here (unregister old + register new).
func (s *vectorStoreService) UpdateStore(ctx context.Context, store *types.VectorStore) error {
	if store.TenantID == 0 {
		return errors.NewValidationError("tenant_id is required")
	}
	if store.Name == "" {
		return errors.NewValidationError("name is required")
	}

	logger.Infof(ctx, "Updating vector store: tenant=%d, id=%s", store.TenantID, store.ID)
	return s.repo.Update(ctx, store)
}

// DeleteStore deletes a vector store by tenant + id.
// Phase 2: KB binding check will be added here.
func (s *vectorStoreService) DeleteStore(ctx context.Context, tenantID uint64, id string) error {
	if err := s.repo.Delete(ctx, tenantID, id); err != nil {
		return err
	}

	// Unregister from registry (idempotent)
	if s.storeRegistry != nil {
		s.storeRegistry.UnregisterByStoreID(id)
	}

	logger.Infof(ctx, "Deleted vector store: tenant=%d, id=%s", tenantID, id)
	return nil
}

// SaveDetectedVersion updates the connection_config.version for a stored vector store.
// Works on a copy to avoid mutating the caller's object.
func (s *vectorStoreService) SaveDetectedVersion(ctx context.Context, store *types.VectorStore, version string) error {
	updated := *store
	updated.ConnectionConfig.Version = version
	return s.repo.UpdateConnectionConfig(ctx, &updated)
}

// registerInRegistry creates an engine service and registers it in the registry.
// Logs and skips on failure — the store is already persisted in DB,
// and will be loaded on next app restart (self-healing).
func (s *vectorStoreService) registerInRegistry(ctx context.Context, store *types.VectorStore) {
	if s.storeRegistry == nil || s.factory == nil {
		return
	}

	// Use a short timeout for engine creation to avoid blocking on unreachable hosts
	// (e.g., gRPC dial to Qdrant/Milvus). The store is already persisted in DB,
	// so it will be loaded on next app restart if this times out.
	factoryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	svc, err := s.factory(factoryCtx, *store)
	if err != nil {
		logger.Warnf(ctx, "Failed to create engine for store %s, will be available after restart: %v", store.ID, err)
		return
	}
	s.storeRegistry.RegisterWithStoreID(store.ID, svc)
}

// validateConnectionConfig validates required fields per engine type.
func validateConnectionConfig(engineType types.RetrieverEngineType, config types.ConnectionConfig) error {
	switch engineType {
	case types.ElasticsearchRetrieverEngineType:
		if config.Addr == "" {
			return errors.NewValidationError("addr is required for elasticsearch")
		}
	case types.PostgresRetrieverEngineType:
		if !config.UseDefaultConnection && config.Addr == "" {
			return errors.NewValidationError("addr or use_default_connection is required for postgres")
		}
	case types.QdrantRetrieverEngineType:
		if config.Host == "" {
			return errors.NewValidationError("host is required for qdrant")
		}
	case types.MilvusRetrieverEngineType:
		if config.Addr == "" {
			return errors.NewValidationError("addr is required for milvus")
		}
	case types.TencentVectorDBRetrieverEngineType:
		if config.Addr == "" {
			return errors.NewValidationError("addr is required for tencent_vectordb")
		}
		if config.Username == "" {
			return errors.NewValidationError("username is required for tencent_vectordb")
		}
		if config.APIKey == "" {
			return errors.NewValidationError("api_key is required for tencent_vectordb")
		}
	case types.WeaviateRetrieverEngineType:
		if config.Host == "" {
			return errors.NewValidationError("host is required for weaviate")
		}
	case types.DorisRetrieverEngineType:
		if config.Addr == "" {
			return errors.NewValidationError("addr is required for doris (FE MySQL host:port)")
		}
		if config.Database == "" {
			return errors.NewValidationError("database is required for doris")
		}
	case types.SQLiteRetrieverEngineType:
		// No connection config needed for SQLite
	}
	return nil
}
