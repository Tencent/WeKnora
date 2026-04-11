package service

import (
	"context"
	"os"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// vectorStoreService implements interfaces.VectorStoreService
type vectorStoreService struct {
	repo interfaces.VectorStoreRepository
}

// NewVectorStoreService creates a new vector store service
func NewVectorStoreService(
	repo interfaces.VectorStoreRepository,
) interfaces.VectorStoreService {
	return &vectorStoreService{repo: repo}
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

	// 5. Persist
	logger.Infof(ctx, "Creating vector store: tenant=%d, name=%s, engine=%s",
		store.TenantID, secutils.SanitizeForLog(store.Name), store.EngineType)
	return s.repo.Create(ctx, store)
}

// UpdateStore updates an existing vector store (name only).
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
	logger.Infof(ctx, "Deleting vector store: tenant=%d, id=%s", tenantID, id)
	return s.repo.Delete(ctx, tenantID, id)
}

// SaveDetectedVersion updates the connection_config.version for a stored vector store.
// Works on a copy to avoid mutating the caller's object.
func (s *vectorStoreService) SaveDetectedVersion(ctx context.Context, store *types.VectorStore, version string) error {
	updated := *store
	updated.ConnectionConfig.Version = version
	return s.repo.UpdateConnectionConfig(ctx, &updated)
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
	case types.WeaviateRetrieverEngineType:
		if config.Host == "" {
			return errors.NewValidationError("host is required for weaviate")
		}
	case types.SQLiteRetrieverEngineType:
		// No connection config needed for SQLite
	}
	return nil
}
