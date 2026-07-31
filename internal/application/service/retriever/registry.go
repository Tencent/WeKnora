package retriever

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// EngineBuildTimeout bounds a single on-demand engine construction. It is
// exported because callers that budget a sequence of resolutions need to size
// their own ceiling above one build; leaving the two coupled only by a comment
// invites them to drift apart.
//
// Not every engine constructor observes the context it is handed, so this
// bounds the ones that dial eagerly rather than every possible backend.
const EngineBuildTimeout = 10 * time.Second

// rebuildCooldown throttles rebuild attempts for a store whose engine just
// failed to build. Without it a backend that stays down costs a full build
// timeout on every request, because collapsing only helps concurrent callers —
// sequential ones each open a new attempt.
const rebuildCooldown = 30 * time.Second

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

	// repo and factory let the registry rebuild an engine that is missing from
	// byStoreID. Both are optional: when either is nil the registry cannot
	// rebuild anything and GetOrLoadByStoreID stays a plain lookup.
	repo    interfaces.VectorStoreRepository
	factory interfaces.EngineFactory
	sf      singleflight.Group

	// storeGen counts every mutation of a store's entry. An on-demand build
	// samples it before starting and publishes only if it has not moved, so a
	// build cannot undo a registration or a removal that landed while it ran.
	storeGen map[string]uint64
	// failedUntil holds the cooldown deadline per store, keyed only by stores
	// that reached a build attempt.
	failedUntil map[string]time.Time
	// onFlightJoin, when set, fires once a caller is attached to a build.
	// Tests need it to order a second caller against a build already running:
	// until a caller is attached there is nothing to observe from outside, and
	// releasing the build too early lets that caller miss it and read the
	// finished engine instead, which looks identical from the results alone.
	onFlightJoin func()
	// flightObserver, when set, reports whether a caller shared its build with
	// other callers. Collapsing is the point of this path but leaves no trace a
	// test can check from the outside: a caller that misses the flight and then
	// finds the finished engine is indistinguishable from one that waited on
	// it. Production leaves this nil.
	flightObserver func(shared bool)
}

// storeGeneration samples the mutation counter for a store.
func (r *RetrieveEngineRegistry) storeGeneration(storeID string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.storeGen[storeID]
}

// registerIfGenUnchanged publishes svc only when the store's entry has not
// been touched since gen was sampled. Reports whether the engine was published.
func (r *RetrieveEngineRegistry) registerIfGenUnchanged(
	storeID string, gen uint64, svc interfaces.RetrieveEngineService,
) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.storeGen[storeID] != gen {
		return false
	}
	r.byStoreID[storeID] = svc
	delete(r.failedUntil, storeID)
	return true
}

// inFailureCooldown reports whether a recent build failure should short-circuit
// another attempt.
func (r *RetrieveEngineRegistry) inFailureCooldown(storeID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	until, exists := r.failedUntil[storeID]
	return exists && time.Now().Before(until)
}

// markBuildFailed starts the cooldown for a store whose engine build failed.
func (r *RetrieveEngineRegistry) markBuildFailed(storeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failedUntil == nil {
		r.failedUntil = make(map[string]time.Time)
	}
	r.failedUntil[storeID] = time.Now().Add(rebuildCooldown)
}

// NewRetrieveEngineRegistry creates a new retrieval engine registry.
//
// repo and factory let the registry rebuild an engine that is missing from its
// store map. Passing nil for either disables that, leaving GetOrLoadByStoreID
// equivalent to GetByStoreID; both are required arguments rather than an
// optional extra so that every construction site has to say which it wants.
func NewRetrieveEngineRegistry(
	repo interfaces.VectorStoreRepository, factory interfaces.EngineFactory,
) interfaces.RetrieveEngineRegistry {
	return &RetrieveEngineRegistry{
		byEngineType: make(map[types.RetrieverEngineType]interfaces.RetrieveEngineService),
		byStoreID:    make(map[string]interfaces.RetrieveEngineService),
		storeGen:     make(map[string]uint64),
		failedUntil:  make(map[string]time.Time),
		repo:         repo,
		factory:      factory,
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
	// Count this alongside unregistrations: an on-demand build that started
	// earlier must not overwrite the entry published here, which would leave
	// the engine this call installed orphaned with its connections open.
	r.bumpGenerationLocked(storeID)
}

// bumpGenerationLocked invalidates any on-demand build already in flight for
// this store. Callers must hold the write lock.
func (r *RetrieveEngineRegistry) bumpGenerationLocked(storeID string) {
	if r.storeGen == nil {
		r.storeGen = make(map[string]uint64)
	}
	r.storeGen[storeID]++
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

// GetOrLoadByStoreID returns the engine for storeID, rebuilding it from the
// database when this process has no entry for it.
//
// When either repo or factory is nil the registry cannot rebuild anything and
// a miss is reported exactly as GetByStoreID reports it.
func (r *RetrieveEngineRegistry) GetOrLoadByStoreID(
	ctx context.Context, tenantID uint64, storeID string,
) (interfaces.RetrieveEngineService, error) {
	if svc, err := r.GetByStoreID(storeID); err == nil {
		return svc, nil
	}
	if r.repo == nil || r.factory == nil {
		return nil, ErrVectorStoreNotFound
	}
	if r.inFailureCooldown(storeID) {
		// A recent build failed; do not spend another timeout on it yet.
		return nil, ErrVectorStoreUnavailable
	}
	// Sampled before the build starts: an unregistration landing after this
	// point must prevent the finished engine from being published.
	gen := r.storeGeneration(storeID)

	// Key by tenant as well as store so that a caller reaching this method
	// without an ownership check cannot join another tenant's flight.
	key := fmt.Sprintf("%d:%s", tenantID, storeID)

	results := r.doChanJoin(key, func() (res interface{}, err error) {
		// singleflight re-raises a panic from this function on a goroutine of
		// its own, out of reach of the HTTP recovery middleware. The build
		// below calls third-party client constructors, so without this a
		// broken one would take down the process instead of failing a request.
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.GetLogger(ctx).Errorf(
					"[retriever.registry] engine build panicked for store %s: %v\n%s",
					storeID, recovered, debug.Stack())
				res, err = nil, ErrVectorStoreUnavailable
			}
		}()

		// Detach from the initiating request. Callers collapse onto one build,
		// so letting the first caller's cancellation abort it would fail every
		// other caller waiting on the same engine.
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), EngineBuildTimeout)
		defer cancel()

		// An earlier flight for this key may have finished after the miss above.
		if svc, lookupErr := r.GetByStoreID(storeID); lookupErr == nil {
			return svc, nil
		}
		store, err := r.repo.GetByID(buildCtx, tenantID, storeID)
		if err != nil {
			// The store may well exist; the metadata database just could not
			// answer. Saying "not found" here would make async workers discard
			// their task over a passing outage.
			logger.GetLogger(ctx).Errorf(
				"[retriever.registry] loading store %s for rebuild failed: %v", storeID, err)
			return nil, ErrVectorStoreUnavailable
		}
		if store == nil {
			return nil, ErrVectorStoreNotFound
		}
		svc, err := r.factory(buildCtx, *store)
		if err != nil {
			// The cause dies with this log: it names the backend endpoint, so
			// it must not travel back to the caller.
			logger.GetLogger(ctx).Errorf(
				"[retriever.registry] rebuilding engine for store %s failed, "+
					"retrying no sooner than %s: %v", storeID, rebuildCooldown, err)
			r.markBuildFailed(storeID)
			return nil, ErrVectorStoreUnavailable
		}
		if svc == nil {
			// Publishing nil would panic every later reader of this store.
			logger.GetLogger(ctx).Errorf(
				"[retriever.registry] engine factory returned no engine for store %s", storeID)
			return nil, ErrVectorStoreUnavailable
		}
		if !r.registerIfGenUnchanged(storeID, gen, svc) {
			// The entry changed while this engine was being built, so this one
			// is stale before it is published. Whatever landed instead is
			// authoritative; the caller retries and picks it up.
			return nil, ErrVectorStoreUnavailable
		}
		return svc, nil
	})

	select {
	case <-ctx.Done():
		// The shared build keeps running for the other callers; this caller
		// simply stops waiting. Reporting the context error rather than the
		// not-found sentinel matters: callers map the sentinel to a permanent
		// failure and would drop retryable work on a shutdown.
		return nil, ctx.Err()
	case result := <-results:
		if r.flightObserver != nil {
			r.flightObserver(result.Shared)
		}
		if result.Err != nil {
			// Already a sentinel: the build logged its own cause, and the raw
			// error is deliberately not carried back to the caller.
			return nil, result.Err
		}
		svc, ok := result.Val.(interfaces.RetrieveEngineService)
		if !ok {
			return nil, ErrVectorStoreUnavailable
		}
		return svc, nil
	}
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
	r.bumpGenerationLocked(storeID)
	// Let an operator retry immediately after removing a store rather than
	// waiting out a cooldown left over from the previous configuration.
	delete(r.failedUntil, storeID)
}

// Compile-time assertion: *RetrieveEngineRegistry satisfies the
// interfaces.RetrieveEngineRegistry contract, including GetByStoreID.
var _ interfaces.RetrieveEngineRegistry = (*RetrieveEngineRegistry)(nil)

// CanRebuildStores reports whether the registry was given what it needs to
// rebuild a store engine on demand. Exposed so that wiring can be asserted:
// a registry without those dependencies still serves lookups, so nothing else
// would reveal that rebuilding was silently left off.
func (r *RetrieveEngineRegistry) CanRebuildStores() bool {
	return r.repo != nil && r.factory != nil
}

// doChanJoin attaches the caller to a build for key, starting one if none is
// running, and reports the attachment to onFlightJoin.
func (r *RetrieveEngineRegistry) doChanJoin(
	key string, build func() (interface{}, error),
) <-chan singleflight.Result {
	results := r.sf.DoChan(key, build)
	if r.onFlightJoin != nil {
		r.onFlightJoin()
	}
	return results
}
