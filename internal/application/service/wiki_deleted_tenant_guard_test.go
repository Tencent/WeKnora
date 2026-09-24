package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wikiTenantGuardPendingRepo stubs the slice of TaskPendingOpsRepository the
// tenant-liveness guard touches: the liveness lookup and the scope cleanup.
type wikiTenantGuardPendingRepo struct {
	interfaces.TaskPendingOpsRepository
	active     bool
	lookupErr  error
	lookups    []uint64
	deletedKBs []string
}

func (r *wikiTenantGuardPendingRepo) HasActiveTenant(_ context.Context, tenantID uint64) (bool, error) {
	r.lookups = append(r.lookups, tenantID)
	return r.active, r.lookupErr
}

func (r *wikiTenantGuardPendingRepo) DeleteByScope(_ context.Context, scope, scopeID string) error {
	if scope == types.TaskScopeKnowledgeBase {
		r.deletedKBs = append(r.deletedKBs, scopeID)
	}
	return nil
}

func TestWikiTaskSkipsDeletedTenant(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{
		TenantID:        7,
		KnowledgeBaseID: "kb-orphaned",
		Language:        "en",
	})
	require.NoError(t, err)

	t.Run("ingest clears queue and does not proceed", func(t *testing.T) {
		repo := &wikiTenantGuardPendingRepo{active: false}
		svc := &wikiIngestService{pendingRepo: repo}

		err := svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, payload))
		require.NoError(t, err)
		require.Len(t, repo.lookups, 1)
		assert.Equal(t, uint64(7), repo.lookups[0])
		assert.Equal(t, []string{"kb-orphaned"}, repo.deletedKBs)
	})

	t.Run("finalize clears queue and does not proceed", func(t *testing.T) {
		repo := &wikiTenantGuardPendingRepo{active: false}
		svc := &wikiIngestService{pendingRepo: repo}

		err := svc.ProcessWikiFinalize(context.Background(), asynq.NewTask(types.TypeWikiFinalize, payload))
		require.NoError(t, err)
		require.Len(t, repo.lookups, 1)
		assert.Equal(t, []string{"kb-orphaned"}, repo.deletedKBs)
	})
}

func TestWikiTenantGuardFailsOpenOnLookupError(t *testing.T) {
	repo := &wikiTenantGuardPendingRepo{active: false, lookupErr: assert.AnError}
	svc := &wikiIngestService{pendingRepo: repo}

	// A transient liveness-lookup failure must not be read as "deleted":
	// the task keeps its retry budget and the enqueue guard / startup
	// recovery enforce the same invariant on their own DB access.
	assert.False(t, svc.tenantIsDeleted(context.Background(), 7))
	assert.Empty(t, repo.deletedKBs)
}

func TestWikiTenantGuardWithoutLivenessCapability(t *testing.T) {
	// A pending repo without the liveness extension (legacy test doubles,
	// alternate implementations) is never treated as a deleted tenant.
	svc := &wikiIngestService{pendingRepo: &wikiKBGuardPendingRepo{}}

	assert.False(t, svc.tenantIsDeleted(context.Background(), 7))
}

func TestWikiTenantGuardIgnoresZeroTenant(t *testing.T) {
	repo := &wikiTenantGuardPendingRepo{active: false}
	svc := &wikiIngestService{pendingRepo: repo}

	assert.False(t, svc.tenantIsDeleted(context.Background(), 0))
	assert.Empty(t, repo.lookups)
	_ = interfaces.TaskPendingOpsTenantLiveness(nil)
}
