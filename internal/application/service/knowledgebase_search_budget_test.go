package service

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ctxRecordingRegistry captures the context handed to the store lookup and
// then fails, so the caller stops before building retrieval params.
type ctxRecordingRegistry struct {
	interfaces.RetrieveEngineRegistry
	seen context.Context
}

func (r *ctxRecordingRegistry) GetByStoreID(string) (interfaces.RetrieveEngineService, error) {
	return nil, stderrors.New("store not in registry")
}

func (r *ctxRecordingRegistry) GetOrLoadByStoreID(
	ctx context.Context, _ uint64, _ string,
) (interfaces.RetrieveEngineService, error) {
	r.seen = ctx
	return nil, stderrors.New("build refused")
}

// TestResolveStoreGroups_BoundsEngineResolution pins the ceiling on engine
// resolution.
//
// Groups are resolved one at a time and each may rebuild a missing engine by
// dialing its backend. This server configures no HTTP read or write timeout,
// so without a budget here a search over several cold stores would run for as
// long as the sum of those builds with nothing to stop it.
func TestResolveStoreGroups_BoundsEngineResolution(t *testing.T) {
	storeID := "00000000-0000-0000-0000-0000000000bb"
	registry := &ctxRecordingRegistry{}
	svc := &knowledgeBaseService{
		retrieveEngine: registry,
		ownership:      &fakeOwnership{owned: map[string]uint64{storeID: 1}},
	}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, VectorStoreID: &storeID}

	_, err := svc.resolveStoreGroups(
		context.Background(), kb, []*types.KnowledgeBase{kb}, types.SearchParams{}, 5)

	require.Error(t, err, "the refused build must surface as an error")
	require.NotNil(t, registry.seen, "the lookup must have been reached")

	deadline, ok := registry.seen.Deadline()
	require.True(t, ok, "engine resolution must run under a deadline")
	// Compared against a literal rather than the constant itself: checking the
	// constant against its own value passes no matter what it is changed to.
	assert.InDelta(t, 12.0, time.Until(deadline).Seconds(), 0.5,
		"the deadline must come from the resolve budget")
}

// TestStoreResolveBudgetExceedsOneBuild pins the relationship the budget
// depends on. Resolution can rebuild an engine, so a budget at or below a
// single build timeout would cut off the first rebuild it is meant to allow
// and turn every cold store into an error.
func TestStoreResolveBudgetExceedsOneBuild(t *testing.T) {
	t.Parallel()
	assert.Greater(t, storeResolveBudget, retriever.EngineBuildTimeout,
		"the search budget must leave room for at least one engine build")
}

// TestResolveStoreGroups_BudgetDoesNotOutliveTheCaller checks that the budget
// only ever shortens the caller's context, never extends it.
func TestResolveStoreGroups_BudgetDoesNotOutliveTheCaller(t *testing.T) {
	storeID := "00000000-0000-0000-0000-0000000000cc"
	registry := &ctxRecordingRegistry{}
	svc := &knowledgeBaseService{
		retrieveEngine: registry,
		ownership:      &fakeOwnership{owned: map[string]uint64{storeID: 1}},
	}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, VectorStoreID: &storeID}

	callerDeadline := time.Now().Add(2 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()

	_, err := svc.resolveStoreGroups(ctx, kb, []*types.KnowledgeBase{kb}, types.SearchParams{}, 5)
	require.Error(t, err)

	deadline, ok := registry.seen.Deadline()
	require.True(t, ok)
	assert.False(t, deadline.After(callerDeadline),
		"a caller that allows less time than the budget must still win")
}
