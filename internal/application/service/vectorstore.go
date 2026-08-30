package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
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

	// 2.1. SSRF validation on user-supplied addresses (whitelist-first).
	// Placed before any network I/O (step 5 TestConnection, step 7 registry
	// factory) so a blocked address never triggers an outbound connection.
	if err := validateConnectionAddrSSRF(store.EngineType, store.ConnectionConfig); err != nil {
		return err
	}

	// 2.5. Index config validation (bounds, name characters)
	if err := types.ValidateIndexConfig(store.IndexConfig); err != nil {
		return err
	}

	// 2.6. Engine-specific index config validation (OpenSearch HNSW bounds).
	// Create-only: UpdateStore mutates just the name, so this is not re-run there.
	if store.EngineType == types.OpenSearchRetrieverEngineType {
		if err := validateOpenSearchIndexConfig(store.IndexConfig); err != nil {
			return err
		}
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
		if database.SupportsRowLock(tx.Dialector.Name()) {
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
// EnvDefaultStoreView is the env-fallback display, enriched with the
// active env store's engine type when one is configured. Exposed
// separately from ResolveStoreView so list paths can fill the
// env-bound entries without invoking the single-KB resolver.
func (s *vectorStoreService) EnvDefaultStoreView(_ context.Context) types.StoreDisplay {
	return s.defaultStoreDisplay()
}

func (s *vectorStoreService) ResolveStoreView(
	ctx context.Context, tenantID uint64, storeID string,
) (types.StoreDisplay, error) {
	if storeID == "" {
		return s.defaultStoreDisplay(), nil
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
			out[id] = s.defaultStoreDisplay()
			continue
		}
		if _, ok := out[id]; !ok {
			out[id] = types.UnavailableStoreDisplay()
		}
	}
	return out, nil
}

// defaultStoreDisplay returns the env-fallback display, enriched with the
// active env store's engine type when one is configured. Callers receive a
// fully populated StoreDisplay so UIs can render the same badge shape for
// env-bound and user-bound KBs (e.g. "postgres" vs "qdrant") without
// branching on Source.
func (s *vectorStoreService) defaultStoreDisplay() types.StoreDisplay {
	d := types.DefaultStoreDisplay()
	if len(s.envStores) > 0 {
		d.EngineType = string(s.envStores[0].EngineType)
	}
	return d
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
	case types.OpenSearchRetrieverEngineType:
		if config.Addr == "" {
			return errors.NewValidationError("addr is required for opensearch")
		}
	case types.SQLiteRetrieverEngineType:
		// No connection config needed for SQLite
	}
	return nil
}

// validateConnectionAddrSSRF validates every user-supplied address field of a
// connection config against the SSRF policy (whitelist first, then the strict
// IP / port / DNS checks inside secutils.ValidateURLForSSRF). It is applied
// ONLY at user-input boundaries — CreateStore and TestRawConnection. Env
// stores and already-stored configs are trusted and intentionally skip it.
//
// Unknown engine types are REJECTED (fail-closed): a newly added engine must
// not be able to reach a dial path without an explicit address mapping here.
// Empty fields are skipped — required-field presence is the responsibility of
// validateConnectionConfig, which runs first on every guarded path.
func validateConnectionAddrSSRF(engineType types.RetrieverEngineType, config types.ConnectionConfig) error {
	// check validates a single address field. Empty fields are no-ops so this
	// helper is independent of required-field enforcement.
	check := func(addr string) error {
		if addr == "" {
			return nil
		}
		if err := secutils.ValidateURLForSSRF(addr); err != nil {
			return errors.NewValidationError(
				secutils.FormatSSRFError("vector store address", addr, err))
		}
		return nil
	}

	switch engineType {
	case types.ElasticsearchRetrieverEngineType,
		types.OpenSearchRetrieverEngineType,
		types.MilvusRetrieverEngineType,
		types.TencentVectorDBRetrieverEngineType,
		types.DorisRetrieverEngineType:
		// Single address field: a URL (es/opensearch) or bare host:port
		// (milvus/tencent/doris). ValidateURLForSSRF normalises both.
		return check(config.Addr)
	case types.QdrantRetrieverEngineType:
		// Host (+ optional Port) — combine so the port blocklist applies to
		// the actual dial target rather than just the bare host.
		addr := config.Host
		if addr != "" && config.Port != 0 {
			addr = fmt.Sprintf("%s:%d", config.Host, config.Port)
		}
		return check(addr)
	case types.WeaviateRetrieverEngineType:
		// Both the HTTP host and the gRPC address are dialed by the driver,
		// so both must be validated (validating Host alone leaves GrpcAddress
		// as an open SSRF vector).
		if err := check(config.Host); err != nil {
			return err
		}
		return check(config.GrpcAddress)
	case types.SQLiteRetrieverEngineType:
		// File-based engine; no remote address to validate.
		return nil
	default:
		// Fail closed. Engines without a DB-store address mapping (postgres,
		// infinity, elasticfaiss, and any future engine) must not silently
		// bypass SSRF validation. The guarded callers (CreateStore,
		// TestRawConnection) already restrict to validEngineTypes, so this is
		// defence-in-depth rather than a user-facing path.
		return errors.NewValidationError(
			fmt.Sprintf("SSRF validation is not configured for engine type: %s", engineType))
	}
}

// TestRawConnection validates raw (unpersisted) user-supplied connection config
// — engine-type allowlist, required fields, then the SSRF policy — before
// delegating to TestConnection. Handlers MUST use this for raw user input
// (e.g. POST /vector-stores/test).
//
// TestConnection itself stays validation-free for trusted callers (env stores
// and stored configs already validated at create time, which legitimately use
// internal hosts such as localhost). Do NOT consolidate the two methods.
func (s *vectorStoreService) TestRawConnection(
	ctx context.Context,
	engineType types.RetrieverEngineType,
	config types.ConnectionConfig,
) (string, error) {
	// 1. Engine-type allowlist. Only DB-registerable engines may be probed
	//    with raw credentials; this blocks e.g. a raw postgres probe against
	//    the application's own database host (a credential oracle).
	if !types.IsValidEngineType(engineType) {
		return "", errors.NewValidationError(
			fmt.Sprintf("connection test is not supported for engine type: %s", engineType))
	}
	// 2. Required fields. Prevents an empty field from falling through to a
	//    driver's internal default (e.g. milvus empty addr -> localhost:19530),
	//    which would otherwise dial an internal host unchecked.
	if err := validateConnectionConfig(engineType, config); err != nil {
		return "", err
	}
	// 3. SSRF policy on every user-supplied address field.
	if err := validateConnectionAddrSSRF(engineType, config); err != nil {
		return "", err
	}
	return s.TestConnection(ctx, engineType, config)
}

// openSearch HNSW bound constants. Shards / replicas are NOT validated here —
// the flat types.ValidateIndexConfig already enforces those caps for every
// engine. These caps mirror the GetVectorStoreTypes Min/Max so the UI and
// backend agree. A zero / empty field means "use the driver default" and is
// always accepted.
const (
	osHNSWMMin              = 2
	osHNSWMMax              = 100
	osHNSWEFConstructionMin = 2
	osHNSWEFConstructionMax = 4096
	osHNSWEFSearchMin       = 1
	osHNSWEFSearchMax       = 10000
)

// validateOpenSearchIndexConfig validates the OpenSearch-specific HNSW fields.
// Called from CreateStore only (the store is create-only; UpdateStore mutates
// just the name). Unset fields (zero / empty) fall back to driver defaults and
// are accepted.
func validateOpenSearchIndexConfig(ic types.IndexConfig) error {
	if ic.HNSWM != 0 && (ic.HNSWM < osHNSWMMin || ic.HNSWM > osHNSWMMax) {
		return errors.NewValidationError(
			fmt.Sprintf("hnsw_m must be between %d and %d", osHNSWMMin, osHNSWMMax))
	}
	if ic.HNSWEFConstruction != 0 &&
		(ic.HNSWEFConstruction < osHNSWEFConstructionMin || ic.HNSWEFConstruction > osHNSWEFConstructionMax) {
		return errors.NewValidationError(
			fmt.Sprintf("hnsw_ef_construction must be between %d and %d", osHNSWEFConstructionMin, osHNSWEFConstructionMax))
	}
	if ic.HNSWEFSearch != 0 &&
		(ic.HNSWEFSearch < osHNSWEFSearchMin || ic.HNSWEFSearch > osHNSWEFSearchMax) {
		return errors.NewValidationError(
			fmt.Sprintf("hnsw_ef_search must be between %d and %d", osHNSWEFSearchMin, osHNSWEFSearchMax))
	}
	if ic.KNNEngine != "" && ic.KNNEngine != "lucene" && ic.KNNEngine != "faiss" {
		return errors.NewValidationError(`knn_engine must be "lucene" or "faiss"`)
	}
	return nil
}
