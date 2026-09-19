package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// vectorStoreGateRegistry is a RetrieveEngineRegistry stub whose store lookup
// always fails with the injected error. The embedded interface keeps the surface
// honest: any method this test does not exercise panics instead of silently
// returning a zero value.
type vectorStoreGateRegistry struct {
	interfaces.RetrieveEngineRegistry
	err error
}

func (r vectorStoreGateRegistry) GetOrLoadByStoreID(
	context.Context, uint64, string,
) (interfaces.RetrieveEngineService, error) {
	if r.err != nil {
		return nil, r.err
	}
	return vectorStoreGateEngine{}, nil
}

// vectorStoreGateEngine is the resolved store the happy path returns. Support()
// is called by the factory when it builds the composite.
type vectorStoreGateEngine struct {
	interfaces.RetrieveEngineService
}

func (vectorStoreGateEngine) Support() []types.RetrieverType { return nil }

// vectorStoreGateOwnership reports every store as owned by the caller so the
// test reaches the registry lookup instead of the cross-tenant branch.
type vectorStoreGateOwnership struct{}

func (vectorStoreGateOwnership) StoreOwnedBy(context.Context, string, uint64) (bool, error) {
	return true, nil
}

// vectorStoreGateRepo records the knowledge writes the gate performs when it
// settles a terminal failure.
type vectorStoreGateRepo struct {
	interfaces.KnowledgeRepository
	writes []*types.Knowledge
}

func (r *vectorStoreGateRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	copied := *knowledge
	r.writes = append(r.writes, &copied)
	return nil
}

func newVectorStoreGateService(err error) (*knowledgeService, *vectorStoreGateRepo) {
	repo := &vectorStoreGateRepo{}
	return &knowledgeService{
		retrieveEngine: vectorStoreGateRegistry{err: err},
		ownership:      vectorStoreGateOwnership{},
		repo:           repo,
	}, repo
}

func indexedGateKB() *types.KnowledgeBase {
	storeID := "store-1"
	return &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		VectorStoreID:    &storeID,
	}
}

// TestEnsureVectorStoreReadySkipsWhenIndexingDisabled keeps KBs that run without
// a vector store (vector + keyword indexing off) working: the gate must not
// resolve an engine for them, so the nil registry here stays untouched.
func TestEnsureVectorStoreReadySkipsWhenIndexingDisabled(t *testing.T) {
	svc := &knowledgeService{}

	require.NoError(t, svc.ensureVectorStoreReady(
		context.Background(), &types.KnowledgeBase{}, &types.Knowledge{ID: "doc"}, 7))
}

// TestEnsureVectorStoreReadyStopsRetryablyBeforeFinalAttempt is the regression
// guard for a vector store that is not reachable yet (an external retriever
// plugin whose backend is still starting): the task must stop before any work
// happens, leave the knowledge untouched, and stay retryable so the deferred
// attempt can succeed once the store is up.
func TestEnsureVectorStoreReadyStopsRetryablyBeforeFinalAttempt(t *testing.T) {
	svc, repo := newVectorStoreGateService(retriever.ErrVectorStoreUnavailable)

	err := svc.ensureVectorStoreReady(
		context.Background(), indexedGateKB(), &types.Knowledge{ID: "doc"}, 7)

	require.ErrorIs(t, err, ErrVectorStoreNotReady)
	require.NotErrorIs(t, err, asynq.SkipRetry)
	require.Empty(t, repo.writes, "a retryable stop must not mark the knowledge failed")
}

// TestEnsureVectorStoreReadySettlesOnFinalAttempt makes sure the retry budget
// cannot leave the row stuck in processing forever: on the last attempt the
// failure is persisted and the task stops retrying.
func TestEnsureVectorStoreReadySettlesOnFinalAttempt(t *testing.T) {
	svc, repo := newVectorStoreGateService(retriever.ErrVectorStoreUnavailable)
	ctx := types.WithTaskRetryMetadata(context.Background(), 3, 3)

	err := svc.ensureVectorStoreReady(ctx, indexedGateKB(), &types.Knowledge{ID: "doc"}, 7)

	require.ErrorIs(t, err, asynq.SkipRetry)
	require.Len(t, repo.writes, 1)
	require.Equal(t, types.ParseStatusFailed, repo.writes[0].ParseStatus)
}

// TestEnsureVectorStoreReadyDoesNotRetryPermanentFailures keeps a missing or
// cross-tenant store from burning the retry budget, mirroring the classification
// the index-cleanup worker applies (tag.go).
func TestEnsureVectorStoreReadyDoesNotRetryPermanentFailures(t *testing.T) {
	svc, repo := newVectorStoreGateService(retriever.ErrVectorStoreNotFound)

	err := svc.ensureVectorStoreReady(
		context.Background(), indexedGateKB(), &types.Knowledge{ID: "doc"}, 7)

	require.ErrorIs(t, err, asynq.SkipRetry)
	require.Len(t, repo.writes, 1)
	require.Equal(t, types.ParseStatusFailed, repo.writes[0].ParseStatus)
}

// TestEnsureVectorStoreReadyAcceptsReachableStore covers the happy path: a store
// that resolves leaves the task running and writes nothing.
func TestEnsureVectorStoreReadyAcceptsReachableStore(t *testing.T) {
	svc, repo := newVectorStoreGateService(nil)

	require.NoError(t, svc.ensureVectorStoreReady(
		context.Background(), indexedGateKB(), &types.Knowledge{ID: "doc"}, 7))
	require.Empty(t, repo.writes)
}
