package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// vectorStoreService implements interfaces.VectorStoreService
type vectorStoreService struct {
	repo          interfaces.VectorStoreRepository
	kbRepo        interfaces.KnowledgeBaseRepository // counts bound KBs for the delete guard
	storeRegistry interfaces.StoreRegistry           // for dynamic registry updates on CRUD
	factory       interfaces.EngineFactory           // creates engine services from VectorStore config
	db            *gorm.DB                           // shared handle for cross-table transactions (delete guard)
	envStores     []types.VectorStore                // env stores derived once at construction for ResolveStoreView fast path
}

// NewVectorStoreService creates a new vector store service.
//
// kbRepo and db are required by the delete guard, which counts bound KBs
// inside a transaction. storeRegistry and factory are optional in tests
// (passing nil disables dynamic registration / unregistration).
func NewVectorStoreService(
	repo interfaces.VectorStoreRepository,
	kbRepo interfaces.KnowledgeBaseRepository,
	storeRegistry interfaces.StoreRegistry,
	factory interfaces.EngineFactory,
	db *gorm.DB,
) interfaces.VectorStoreService {
	return &vectorStoreService{
		repo:          repo,
		kbRepo:        kbRepo,
		storeRegistry: storeRegistry,
		factory:       factory,
		db:            db,
		// Cache the env-store derivation once at construction so per-request
		// resolution does not re-read os environment variables every call.
		envStores: types.BuildEnvVectorStores(os.Getenv("RETRIEVE_DRIVER"), os.Getenv),
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

	// 4. Duplicate check — env stores. We re-derive on each create because
	// CreateStore is a low-frequency admin action; consistency with the
	// startup-cached envStores is enforced by RETRIEVE_DRIVER being read
	// only at process start.
	for _, envStore := range s.envStores {
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

	// 7. Register in registry (best-effort; failure doesn't roll back DB).
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

// DeleteStore deletes a vector store by tenant + id, after verifying that no
// knowledge base is currently bound to it.
//
// Guard rules:
//
//  1. Run inside a transaction so that the binding count and the store
//     delete are atomic with respect to other writers holding the store
//     row lock. Default isolation is Read Committed; this is a write-lock
//     relationship, not a "shared snapshot" relationship.
//  2. PostgreSQL: take a row-level X-lock on the vector_stores row via
//     SELECT … FOR UPDATE so concurrent KB-create requests reading the
//     same store row block until our transaction completes. SQLite
//     serializes writes via WAL + max-open-conns=1, so the lock hint is
//     skipped and we rely on the transaction boundary alone.
//  3. Count knowledge_bases rows via the shared CountByVectorStoreID
//     repository method (tx-aware), which leverages the composite index
//     (tenant_id, vector_store_id). GORM auto-applies the soft-delete
//     scope — no explicit deleted_at predicate is needed.
//  4. After commit, unregister from the in-memory registry. Wrapped in
//     defer/recover so a panic in UnregisterByStoreID surfaces as a
//     structured warning instead of silently leaking the stale engine.
//
// Race window remaining:
//
//	A narrow window exists between CreateKnowledgeBase's binding check and
//	the INSERT — a KB can be created against a store that is simultaneously
//	being deleted. The retrieve-engine factory then rejects searches with
//	the ErrVectorStoreForbidden / NotFound sentinel; the KB response view
//	surfaces the condition through vector_store_status="unavailable" so the
//	UI can guide recovery (admin tool / rebind / KB recreation).
//
// Multi-replica registry staleness:
//
//	The in-memory registry is per-process. After a successful commit +
//	UnregisterByStoreID on this replica, sibling replicas continue serving
//	the engine from their own caches until process restart. This method
//	does not broadcast invalidation across the cluster.
func (s *vectorStoreService) DeleteStore(ctx context.Context, tenantID uint64, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// tx inherits ctx from WithContext above; no need to re-attach.

		// 1. Lock the store row (PG row-level X-lock; skipped on SQLite).
		var store types.VectorStore
		q := tx.Where("id = ? AND tenant_id = ?", id, tenantID)
		if s.isPostgres(tx) {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.First(&store).Error; err != nil {
			if stderrors.Is(err, gorm.ErrRecordNotFound) {
				return errors.NewNotFoundError("vector store not found")
			}
			return err
		}

		// 2. Binding count under the same write-lock boundary.
		count, err := s.kbRepo.CountByVectorStoreID(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return errors.NewBadRequestError(
				fmt.Sprintf(
					"vector store still has %d knowledge base(s) bound to it; "+
						"unbind or delete them before removing the store", count))
		}

		// 3. Soft-delete (gorm.DeletedAt fills automatically).
		return tx.Delete(&store).Error
	})
	if err != nil {
		return err
	}

	// 4. Unregister from registry — wrapped to convert panics into ops
	//    warnings rather than silent stale-engine leaks.
	s.unregisterSafely(ctx, id)
	logger.Infof(ctx, "Deleted vector store: tenant=%d, id=%s", tenantID,
		secutils.SanitizeForLog(id))
	return nil
}

// unregisterSafely calls the registry's idempotent unregister with panic
// containment. A panic here is recoverable because the registry is
// in-memory and self-heals on process restart — but it must be loud
// enough for ops to purge the stale engine on running replicas.
func (s *vectorStoreService) unregisterSafely(ctx context.Context, storeID string) {
	defer func() {
		if r := recover(); r != nil {
			logger.WarnWithFields(ctx, logger.Fields{
				"store_id": secutils.SanitizeForLog(storeID),
				"panic":    fmt.Sprint(r),
			}, "[vectorstore.delete] registry unregister panicked; engine may stay stale until restart")
		}
	}()
	if s.storeRegistry != nil {
		s.storeRegistry.UnregisterByStoreID(storeID)
	}
}

// isPostgres reports whether the active GORM dialector is PostgreSQL.
// Used to gate dialect-specific clauses (e.g., SELECT FOR UPDATE) that
// SQLite would either ignore (recent versions) or fail to compile on.
func (s *vectorStoreService) isPostgres(db *gorm.DB) bool {
	return db != nil && db.Dialector != nil && db.Dialector.Name() == "postgres"
}

// SaveDetectedVersion updates the connection_config.version for a stored vector store.
// Works on a copy to avoid mutating the caller's object.
func (s *vectorStoreService) SaveDetectedVersion(ctx context.Context, store *types.VectorStore, version string) error {
	updated := *store
	updated.ConnectionConfig.Version = version
	return s.repo.UpdateConnectionConfig(ctx, &updated)
}

// ResolveStoreView returns the API-safe display projection of a single
// store ID for embedding in another resource's response (typically a KB).
//
// Resolution order:
//
//  1. storeID == "" → DefaultStoreDisplay (env fallback semantics).
//  2. DB store row matching (id, tenantID) → user-source display.
//  3. Cached env store with matching ID → env-source display.
//  4. Otherwise → UnavailableStoreDisplay with a structured warn log.
//
// Errors from the underlying repository are returned to the caller so
// transient infrastructure failures can be classified, but the returned
// StoreDisplay is still UnavailableStoreDisplay so a handler that ignores
// the error degrades gracefully rather than panicking on a zero value.
func (s *vectorStoreService) ResolveStoreView(
	ctx context.Context, tenantID uint64, storeID string,
) (types.StoreDisplay, error) {
	if storeID == "" {
		return types.DefaultStoreDisplay(), nil
	}
	store, err := s.repo.GetByID(ctx, tenantID, storeID)
	if err != nil {
		return types.UnavailableStoreDisplay(), err
	}
	if store != nil {
		return types.StoreDisplay{
			Name:       store.Name,
			Source:     types.StoreSourceUser,
			EngineType: string(store.EngineType),
			Status:     "available",
		}, nil
	}
	for _, env := range s.envStores {
		if env.ID == storeID {
			return types.StoreDisplay{
				Name:       env.Name,
				Source:     types.StoreSourceEnv,
				EngineType: string(env.EngineType),
				Status:     "available",
			}, nil
		}
	}
	logger.WarnWithFields(ctx, logger.Fields{
		"tenant_id": tenantID,
		"store_id":  secutils.SanitizeForLog(storeID),
	}, "[vectorstore.resolve] bound store missing from DB and env set")
	return types.UnavailableStoreDisplay(), nil
}

// BatchResolveStoreView resolves multiple store IDs in a single DB read
// plus the cached env-store match. Returned map keys are the storeIDs
// originally requested; missing IDs map to UnavailableStoreDisplay.
//
// Intended for list endpoints that need store metadata for many KBs at
// once without incurring N+1 ResolveStoreView calls.
//
// Implementation note: the tenant-store count is bounded by operator
// config (typically tens), so iterating the tenant's full store list
// once is cheaper than a SELECT … WHERE id IN (…) round-trip and avoids
// adding a batch-by-ids repository method that has no other caller.
func (s *vectorStoreService) BatchResolveStoreView(
	ctx context.Context, tenantID uint64, storeIDs []string,
) (map[string]types.StoreDisplay, error) {
	out := make(map[string]types.StoreDisplay, len(storeIDs))
	if len(storeIDs) == 0 {
		return out, nil
	}

	requested := make(map[string]bool, len(storeIDs))
	hasNonEmpty := false
	for _, id := range storeIDs {
		if id == "" {
			continue
		}
		requested[id] = true
		hasNonEmpty = true
	}

	if hasNonEmpty {
		dbStores, err := s.repo.List(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		for _, st := range dbStores {
			if requested[st.ID] {
				out[st.ID] = types.StoreDisplay{
					Name:       st.Name,
					Source:     types.StoreSourceUser,
					EngineType: string(st.EngineType),
					Status:     "available",
				}
			}
		}
		for _, env := range s.envStores {
			if _, ok := out[env.ID]; ok {
				continue
			}
			if requested[env.ID] {
				out[env.ID] = types.StoreDisplay{
					Name:       env.Name,
					Source:     types.StoreSourceEnv,
					EngineType: string(env.EngineType),
					Status:     "available",
				}
			}
		}
	}

	// Fill misses (including empty-string entries) with the appropriate
	// sentinel so callers can rely on a key for every requested ID.
	for _, id := range storeIDs {
		if id == "" {
			out[id] = types.DefaultStoreDisplay()
			continue
		}
		if _, ok := out[id]; !ok {
			out[id] = types.UnavailableStoreDisplay()
		}
	}
	return out, nil
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
