package retriever

import (
	"context"
	"errors"
	"slices"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Sentinel errors returned by factory functions. Callers may use errors.Is to
// classify. User-facing responses MUST wrap or replace these with generic
// messages — the sentinels intentionally omit store UUIDs to avoid enumeration
// leaks. Structured logs inside the factory record the tenant/store IDs.
var (
	// ErrTenantInfoMissing is returned when the factory needs a tenant from
	// context (synchronous, unbound KB path) and none is present.
	ErrTenantInfoMissing = errors.New("tenant info not found in context")

	// ErrVectorStoreNotFound is returned when the store ID is not registered
	// (or an internal lookup for ownership failed). Async workers should treat
	// this as non-retryable.
	ErrVectorStoreNotFound = errors.New("vector store not available")

	// ErrVectorStoreForbidden is returned when the resolved store is not
	// owned by the given tenant. This guards against cross-tenant access
	// in case the upstream validation layer has a gap. Async workers should
	// treat this as non-retryable.
	ErrVectorStoreForbidden = errors.New("vector store access denied")
)

// TenantStoreOwnership abstracts the lookup used by factory functions to
// verify that a given vector store ID is owned by the given tenant ID.
//
// Production implementations wrap the VectorStoreRepository; tests inject
// in-memory fakes so they can cover the ownership branches without touching
// a database.
type TenantStoreOwnership interface {
	// StoreOwnedBy reports whether the store with the given ID is owned
	// by the given tenant. When the store does not exist, it returns
	// (false, nil). Errors are reserved for infrastructure failures such as
	// database connectivity issues.
	StoreOwnedBy(ctx context.Context, storeID string, tenantID uint64) (bool, error)
}

// CreateRetrieveEngineForKB returns a CompositeRetrieveEngine resolved from
// a KB's VectorStore binding.
//
// Resolution rules:
//
//   - vectorStoreID == nil || *vectorStoreID == "" →
//     falls back to the tenant's effective engines (env-store flow driven
//     by RETRIEVE_DRIVER). TenantInfo is read from ctx.
//   - otherwise →
//     1) ownership.StoreOwnedBy(*storeID, tenantID) must return true;
//     cross-tenant attempts yield ErrVectorStoreForbidden.
//     2) registry.GetByStoreID(*storeID) must succeed;
//     unregistered stores yield ErrVectorStoreNotFound.
//     3) the single engine is wrapped by NewCompositeRetrieveEngine so
//     that its Support()-based retriever-type matching is preserved.
//
// Use this for 23 synchronous call sites across the application services.
// Async task handlers that cannot rely on ctx-based TenantInfo (currently:
// ProcessKBDeleteTask, ProcessIndexDelete) must use
// CreateRetrieveEngineFromPayload instead.
func CreateRetrieveEngineForKB(
	ctx context.Context,
	registry interfaces.RetrieveEngineRegistry,
	ownership TenantStoreOwnership,
	tenantID uint64,
	vectorStoreID *string,
) (*CompositeRetrieveEngine, error) {
	// Normalize nil and empty-string pointer to "unbound" so that callers
	// cannot accidentally route an empty UUID into GetByStoreID.
	if vectorStoreID == nil || *vectorStoreID == "" {
		tenantInfo, ok := types.TenantInfoFromContext(ctx)
		if !ok {
			return nil, ErrTenantInfoMissing
		}
		return NewCompositeRetrieveEngine(registry, tenantInfo.GetEffectiveEngines())
	}

	return resolveBoundEngine(ctx, registry, ownership, tenantID, *vectorStoreID)
}

// CreateRetrieveEngineFromPayload is the async-task variant. It does not
// read TenantInfo from ctx because async handlers do not populate it.
// Instead, tenantID is passed explicitly from the deserialized payload and
// is verified against the store's tenant when vectorStoreID is non-empty.
//
// Tasks enqueued before vectorStoreID was added to the payload decode it as
// nil and transparently fall back to the pre-serialized effectiveEngines
// path — no in-flight task is lost across upgrades.
func CreateRetrieveEngineFromPayload(
	ctx context.Context,
	registry interfaces.RetrieveEngineRegistry,
	ownership TenantStoreOwnership,
	tenantID uint64,
	effectiveEngines []types.RetrieverEngineParams,
	vectorStoreID *string,
) (*CompositeRetrieveEngine, error) {
	if vectorStoreID == nil || *vectorStoreID == "" {
		return NewCompositeRetrieveEngine(registry, effectiveEngines)
	}

	return resolveBoundEngine(ctx, registry, ownership, tenantID, *vectorStoreID)
}

// resolveBoundEngine is the shared ownership-verified lookup path used by
// both CreateRetrieveEngineForKB and CreateRetrieveEngineFromPayload. It
// returns sentinel errors so that handlers can classify them (for example,
// async workers convert Forbidden/NotFound into asynq.SkipRetry).
func resolveBoundEngine(
	ctx context.Context,
	registry interfaces.RetrieveEngineRegistry,
	ownership TenantStoreOwnership,
	tenantID uint64,
	storeID string,
) (*CompositeRetrieveEngine, error) {
	owned, err := ownership.StoreOwnedBy(ctx, storeID, tenantID)
	if err != nil {
		// Infrastructure failure — record the raw error for operators but
		// do not leak internals to the caller.
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
			"store_id":  storeID,
			"reason":    "ownership lookup failed",
		})
		return nil, ErrVectorStoreNotFound
	}
	if !owned {
		// Cross-tenant attempt (or the store has been deleted in the
		// meantime). Log with WARN so that audits can surface probing.
		logger.Warnf(ctx,
			"[retriever.factory] cross-tenant store access attempted: tenant=%d store=%s",
			tenantID, storeID)
		return nil, ErrVectorStoreForbidden
	}

	svc, err := registry.GetByStoreID(storeID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
			"store_id":  storeID,
			"reason":    "store not registered",
		})
		return nil, ErrVectorStoreNotFound
	}

	// Build the composite directly from the resolved service.
	//
	// We cannot delegate to NewCompositeRetrieveEngine here because that
	// function resolves engines through registry.GetRetrieveEngineService,
	// which reads from the byEngineType map (env stores). DB stores live
	// in the byStoreID map and are not reachable via engine type alone —
	// multiple stores can share the same engine type.
	//
	// Semantics: a KB bound to a DB store uses every retriever type that
	// store supports. This intentionally overrides the tenant-level
	// effective-engines filter, because binding a KB to a specific store
	// is an explicit opt-out of tenant-default routing.
	return &CompositeRetrieveEngine{
		engineInfos: []*engineInfo{{
			retrieveEngine: svc,
			retrieverType:  slices.Clone(svc.Support()),
		}},
	}, nil
}
