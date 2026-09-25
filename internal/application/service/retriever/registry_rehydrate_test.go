package retriever

import (
	"context"
	stderrors "errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rehydrateStoreID = "00000000-0000-0000-0000-0000000000aa"

// fakeStoreRepo serves a single store by ID. Embedding the interface keeps the
// fake to the one method the rehydration path uses; any other call would
// nil-panic loudly rather than silently returning a zero value.
type fakeStoreRepo struct {
	interfaces.VectorStoreRepository
	store *types.VectorStore
	err   error
}

func (f *fakeStoreRepo) GetByID(_ context.Context, _ uint64, _ string) (*types.VectorStore, error) {
	return f.store, f.err
}

// blockingFactory models a real engine constructor: it dials, which means it
// observes the context it is handed and aborts when that context is cancelled.
type blockingFactory struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newBlockingFactory() *blockingFactory {
	return &blockingFactory{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingFactory) build(ctx context.Context, _ types.VectorStore) (
	interfaces.RetrieveEngineService, error,
) {
	b.calls.Add(1)
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
		return &mockEngineService{engineType: types.ElasticsearchRetrieverEngineType}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newRehydratingRegistry(factory interfaces.EngineFactory) *RetrieveEngineRegistry {
	return &RetrieveEngineRegistry{
		byEngineType: make(map[types.RetrieverEngineType]interfaces.RetrieveEngineService),
		byStoreID:    make(map[string]interfaces.RetrieveEngineService),
		repo:         &fakeStoreRepo{store: &types.VectorStore{ID: rehydrateStoreID}},
		factory:      factory,
	}
}

// TestGetOrLoadByStoreID_LeaderCancelDoesNotPoisonWaiters pins the reason this
// path uses DoChan with a detached build context.
//
// Two requests miss the registry for the same store, so they collapse into one
// engine build. If that shared build inherits the first caller's context, the
// first caller going away — a closed browser tab, a shutting-down worker —
// aborts the build and hands its cancellation error to every other caller
// waiting on it. Those callers are alive and would then be told the store is
// unavailable, which is the very failure this rehydration exists to remove.
func TestGetOrLoadByStoreID_LeaderCancelDoesNotPoisonWaiters(t *testing.T) {
	factory := newBlockingFactory()
	registry := newRehydratingRegistry(factory.build)
	// Installed before any caller starts: the hooks are plain fields, and a
	// caller attaches to its build before the build itself reports progress,
	// so setting them later both races and misses the first attachment.
	shared := make(chan bool, 4)
	registry.flightObserver = func(s bool) { shared <- s }
	joined := make(chan struct{}, 4)
	registry.onFlightJoin = func() { joined <- struct{}{} }

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()

	var leaderErr error
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		_, leaderErr = registry.GetOrLoadByStoreID(leaderCtx, 1, rehydrateStoreID)
	}()

	<-factory.entered // the build is running
	<-joined          // ...and the leader is attached to it

	var (
		waiterSvc interfaces.RetrieveEngineService
		waiterErr error
	)
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		waiterSvc, waiterErr = registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	}()

	// Wait for the waiter to attach before letting the build finish. Released
	// any earlier, the build would complete and register, and the waiter would
	// read that engine directly instead of ever sharing the build — which the
	// result assertions alone cannot tell apart.
	<-joined

	cancelLeader()
	<-leaderDone

	close(factory.release)
	<-waiterDone

	require.NoError(t, waiterErr, "waiter must not inherit the leader's cancellation")
	require.NotNil(t, waiterSvc, "waiter must receive the engine the shared build produced")
	assert.ErrorIs(t, leaderErr, context.Canceled, "the cancelled leader still reports its own cancellation")
	assert.Equal(t, int32(1), factory.calls.Load(), "the waiter must join the flight, not start a second build")
	require.Len(t, shared, 1, "only the waiter reaches the result branch; the leader left on its context")
	assert.Empty(t, joined, "exactly two attachments, both accounted for above")
	assert.True(t, <-shared, "the waiter must have waited on the leader's build, not run its own")

	svc, err := registry.GetByStoreID(rehydrateStoreID)
	require.NoError(t, err, "a completed build must leave the engine registered")
	assert.NotNil(t, svc)
}

// TestGetOrLoadByStoreID_ConcurrentMissesBuildOnce checks that a burst of
// requests for the same missing store produces one engine, not one per caller.
// Engine construction dials a backend, so duplicating it per request would turn
// a cold store into a connection storm.
func TestGetOrLoadByStoreID_ConcurrentMissesBuildOnce(t *testing.T) {
	factory := newBlockingFactory()
	registry := newRehydratingRegistry(factory.build)
	close(factory.release) // let every build finish immediately

	const callers = 16
	var wg sync.WaitGroup
	services := make([]interfaces.RetrieveEngineService, callers)
	errs := make([]error, callers)

	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			services[i], errs[i] = registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
		}()
	}
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i], "caller %d", i)
		require.NotNil(t, services[i], "caller %d", i)
		assert.Same(t, services[0], services[i], "every caller must get the same engine")
	}
	assert.Equal(t, int32(1), factory.calls.Load(), "one build for the whole burst")
}

// TestGetOrLoadByStoreID_FactoryPanicDoesNotCrashProcess pins the panic guard.
// singleflight re-raises a panic from the shared build on its own goroutine,
// where the HTTP recovery middleware cannot catch it, so an unguarded panic
// from a third-party client constructor would end the process rather than the
// request. Reaching the assertions below at all is the real assertion.
func TestGetOrLoadByStoreID_FactoryPanicDoesNotCrashProcess(t *testing.T) {
	registry := newRehydratingRegistry(
		func(context.Context, types.VectorStore) (interfaces.RetrieveEngineService, error) {
			panic("client constructor exploded")
		},
	)

	svc, err := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)

	require.ErrorIs(t, err, ErrVectorStoreUnavailable, "a panicking build is reported as retryable")
	assert.Nil(t, svc)
	_, lookupErr := registry.GetByStoreID(rehydrateStoreID)
	assert.Error(t, lookupErr, "a panicking build must leave nothing registered")
}

// TestGetOrLoadByStoreID_UnregisterDuringBuildIsNotUndone covers the delete
// race: a store removed while its engine is still being built must stay
// removed, otherwise the finishing build silently resurrects it.
func TestGetOrLoadByStoreID_UnregisterDuringBuildIsNotUndone(t *testing.T) {
	factory := newBlockingFactory()
	registry := newRehydratingRegistry(factory.build)

	var (
		svc interfaces.RetrieveEngineService
		err error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc, err = registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	}()

	<-factory.entered
	registry.UnregisterByStoreID(rehydrateStoreID) // delete lands mid-build
	close(factory.release)
	<-done

	require.ErrorIs(t, err, ErrVectorStoreUnavailable, "a build overtaken by a delete must not succeed")
	assert.Nil(t, svc)
	_, lookupErr := registry.GetByStoreID(rehydrateStoreID)
	assert.Error(t, lookupErr, "the deleted store must not be back in the registry")
}

// TestGetOrLoadByStoreID_FailedBuildEntersCooldown checks that a store whose
// build just failed is not retried on the next request. A backend that stays
// down would otherwise cost a full build timeout per request, in exactly the
// outage this rehydration is meant to soften.
func TestGetOrLoadByStoreID_FailedBuildEntersCooldown(t *testing.T) {
	var calls atomic.Int32
	registry := newRehydratingRegistry(
		func(context.Context, types.VectorStore) (interfaces.RetrieveEngineService, error) {
			calls.Add(1)
			return nil, assert.AnError
		},
	)

	_, firstErr := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	_, secondErr := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)

	require.ErrorIs(t, firstErr, ErrVectorStoreUnavailable)
	require.ErrorIs(t, secondErr, ErrVectorStoreUnavailable, "raw build errors must not leak past the sentinel")
	assert.Equal(t, int32(1), calls.Load(), "the second request is served by the cooldown, not a new build")

	// Removing the store clears the cooldown so an operator can retry at once.
	registry.UnregisterByStoreID(rehydrateStoreID)
	_, thirdErr := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	require.Error(t, thirdErr)
	assert.Equal(t, int32(2), calls.Load(), "unregistering must clear the cooldown")
}

// TestGetOrLoadByStoreID_WithoutRepoOrFactoryIsPlainLookup pins the degraded
// mode: a registry built without the rebuild dependencies must behave exactly
// as it did before rehydration existed.
func TestGetOrLoadByStoreID_WithoutRepoOrFactoryIsPlainLookup(t *testing.T) {
	registry := &RetrieveEngineRegistry{
		byEngineType: make(map[types.RetrieverEngineType]interfaces.RetrieveEngineService),
		byStoreID:    make(map[string]interfaces.RetrieveEngineService),
	}

	_, err := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	require.ErrorIs(t, err, ErrVectorStoreNotFound)

	registered := &mockEngineService{engineType: types.ElasticsearchRetrieverEngineType}
	registry.RegisterWithStoreID(rehydrateStoreID, registered)
	svc, err := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	require.NoError(t, err)
	assert.Same(t, registered, svc, "a hit must not consult the database")
}

// cancellingRegistry reports a caller-side cancellation from the rebuild path,
// the way the real registry does when the request that was waiting goes away.
// The lookup still misses, so the factory must reach the rebuild path first.
type cancellingRegistry struct {
	interfaces.RetrieveEngineRegistry
}

func (cancellingRegistry) GetByStoreID(string) (interfaces.RetrieveEngineService, error) {
	return nil, stderrors.New("store not in registry")
}

func (cancellingRegistry) GetOrLoadByStoreID(
	context.Context, uint64, string,
) (interfaces.RetrieveEngineService, error) {
	return nil, context.Canceled
}

// TestBoundLookup_CancellationIsNotReportedAsMissingStore keeps a cancellation
// from being reported as a missing store.
//
// The async delete handlers treat ErrVectorStoreNotFound as permanent and
// answer it with asynq.SkipRetry. A shutdown cancels in-flight handlers, so
// folding that cancellation into the sentinel would make a rolling restart
// discard knowledge-base and index delete tasks that should have been retried,
// leaving vector data behind with nothing scheduled to remove it.
func TestBoundLookup_CancellationIsNotReportedAsMissingStore(t *testing.T) {
	ownership := &fakeOwnership{owned: map[string]uint64{rehydrateStoreID: 1}}
	storeID := rehydrateStoreID

	t.Run("CreateRetrieveEngineForKB", func(t *testing.T) {
		_, err := CreateRetrieveEngineForKB(
			context.Background(), cancellingRegistry{}, ownership, 1, &storeID)

		require.ErrorIs(t, err, context.Canceled, "the cancellation must reach the caller")
		require.NotErrorIs(t, err, ErrVectorStoreNotFound,
			"async handlers map this sentinel to SkipRetry and would drop the task")
	})

	t.Run("VerifyBinding", func(t *testing.T) {
		err := VerifyBinding(context.Background(), cancellingRegistry{}, ownership, 1, storeID)

		require.ErrorIs(t, err, context.Canceled, "the cancellation must reach the caller")
		require.NotErrorIs(t, err, ErrVectorStoreNotFound,
			"async handlers map this sentinel to SkipRetry and would drop the task")
	})
}

// TestGetOrLoadByStoreID_DatabaseFailureIsRetryable separates "the database
// could not answer" from "the store does not exist".
//
// The repository reports a missing row as (nil, nil) and an outage as an
// error, and only the first is permanent. Reporting an outage as not-found
// would make the async delete handlers answer a passing blip with SkipRetry
// and discard work that had nothing wrong with it.
func TestGetOrLoadByStoreID_DatabaseFailureIsRetryable(t *testing.T) {
	registry := newRehydratingRegistry(
		func(context.Context, types.VectorStore) (interfaces.RetrieveEngineService, error) {
			t.Fatal("the engine factory must not run when the store could not be loaded")
			return nil, nil
		},
	)
	registry.repo = &fakeStoreRepo{err: stderrors.New("connection refused")}

	_, err := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)

	require.ErrorIs(t, err, ErrVectorStoreUnavailable, "an outage is retryable")
	require.NotErrorIs(t, err, ErrVectorStoreNotFound,
		"async handlers stop retrying on not-found and would drop the task")
}

// TestGetOrLoadByStoreID_AbsentStoreIsNotFound is the other half: a store that
// genuinely is not there must stay permanent, so workers stop instead of
// retrying something that can never succeed.
func TestGetOrLoadByStoreID_AbsentStoreIsNotFound(t *testing.T) {
	registry := newRehydratingRegistry(
		func(context.Context, types.VectorStore) (interfaces.RetrieveEngineService, error) {
			t.Fatal("the engine factory must not run for a store that does not exist")
			return nil, nil
		},
	)
	registry.repo = &fakeStoreRepo{store: nil}

	_, err := registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)

	require.ErrorIs(t, err, ErrVectorStoreNotFound)
	require.NotErrorIs(t, err, ErrVectorStoreUnavailable)
}

// TestBoundLookup_OwnershipCancellationIsNotAStoreVerdict covers the branch
// that runs before the registry is ever consulted.
//
// The ownership check queries the database with the caller's context, so on a
// shutdown it is normally the first place the cancellation is seen. Reporting
// it as a store verdict there would let the async handlers discard the task
// just as surely as reporting it later would.
func TestBoundLookup_OwnershipCancellationIsNotAStoreVerdict(t *testing.T) {
	ownership := &fakeOwnership{err: context.Canceled}
	storeID := rehydrateStoreID

	_, err := CreateRetrieveEngineForKB(
		context.Background(), newRehydratingRegistry(nil), ownership, 1, &storeID)

	require.ErrorIs(t, err, context.Canceled, "the cancellation must reach the caller")
	require.NotErrorIs(t, err, ErrVectorStoreNotFound,
		"async handlers map this sentinel to SkipRetry and would drop the task")
	require.NotErrorIs(t, err, ErrVectorStoreUnavailable,
		"a cancellation is not a statement about the store")
}

// TestBoundLookup_OwnershipOutageIsRetryable is the neighbouring case: the
// database failed for a reason of its own, which says nothing about whether
// the store exists, so the task deserves another attempt.
func TestBoundLookup_OwnershipOutageIsRetryable(t *testing.T) {
	ownership := &fakeOwnership{err: stderrors.New("connection refused")}
	storeID := rehydrateStoreID

	_, err := CreateRetrieveEngineForKB(
		context.Background(), newRehydratingRegistry(nil), ownership, 1, &storeID)

	require.ErrorIs(t, err, ErrVectorStoreUnavailable)
	require.NotErrorIs(t, err, ErrVectorStoreNotFound)
}

// TestGetOrLoadByStoreID_BuildDoesNotOverwriteAConcurrentRegistration guards
// the create path against the rebuild path.
//
// Registering a store publishes its engine directly. If a rebuild that started
// earlier finished afterwards and published its own, the engine callers had
// just been given would be replaced by a second one built from the same row —
// and the first would be left running with its connections open and nobody
// holding it.
func TestGetOrLoadByStoreID_BuildDoesNotOverwriteAConcurrentRegistration(t *testing.T) {
	factory := newBlockingFactory()
	registry := newRehydratingRegistry(factory.build)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = registry.GetOrLoadByStoreID(context.Background(), 1, rehydrateStoreID)
	}()

	<-factory.entered
	registered := &mockEngineService{engineType: types.PostgresRetrieverEngineType}
	registry.RegisterWithStoreID(rehydrateStoreID, registered)
	close(factory.release)
	<-done

	live, err := registry.GetByStoreID(rehydrateStoreID)
	require.NoError(t, err)
	assert.Same(t, registered, live,
		"the engine published by the registration must survive the finishing build")
}
