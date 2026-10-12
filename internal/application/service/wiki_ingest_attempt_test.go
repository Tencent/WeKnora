package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The op records the parse attempt whose finalizing counter owns its slot,
// so a consumer can tell an op that outlived a reparse from a current one.
func TestNewWikiIngestPendingOpRecordsAttempt(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"attempt from context", withAttempt(context.Background(), 5), 5},
		{"no attempt", context.Background(), 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pendingOp, err := newWikiIngestPendingOp(test.ctx, 7, "kb-1", "knowledge-1")
			require.NoError(t, err)
			var op WikiPendingOp
			require.NoError(t, json.Unmarshal(pendingOp.Payload, &op))
			assert.Equal(t, test.want, op.Attempt)
		})
	}
}

// An op from a superseded attempt must not drain: reparse zeroed that
// attempt's counter, so the drain would release a slot of the new run and
// promote it before its own enrichment finished.
func TestFinalizeWikiSubtaskSkipsSupersededAttempt(t *testing.T) {
	tests := []struct {
		name       string
		attempt    int
		wantDrains int
	}{
		{name: "superseded attempt", attempt: 1},
		{name: "current attempt", attempt: 2, wantDrains: 1},
		{name: "op queued before attempts were recorded", attempt: 0, wantDrains: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &flakyFinalizeRepo{}
			svc := &wikiIngestService{knowledgeRepo: repo, spanTracker: newAttemptTracker(2)}

			svc.finalizeWikiSubtask(context.Background(), "k-1", test.attempt)

			assert.Equal(t, test.wantDrains, repo.successes)
		})
	}
}

// A synthesis model that exists but cannot be built fails identically on
// every trigger and before any op is claimed, so no op ever spends its retry
// budget. On the last delivery the KB's pending ingest ops are released
// instead of holding their documents in "finalizing"; earlier deliveries
// still retry.
func TestWikiIngestReleasesOpsWhenModelUnusableOnFinalAttempt(t *testing.T) {
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"})
	require.NoError(t, err)
	buildErr := errors.New("base URL rejected by SSRF guard")
	tests := []struct {
		name        string
		retried     int
		wantErr     bool
		wantDrained bool
	}{
		{name: "final attempt", retried: 3, wantDrained: true},
		{name: "retry remains", retried: 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pending := &wikiUnavailablePendingRepo{drainKeys: []string{"k-1"}}
			svc := &wikiIngestService{
				kbService: &wikiGuardKBService{kb: &types.KnowledgeBase{
					ID: "kb-1", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
					WikiConfig: &types.WikiConfig{SynthesisModelID: "m-1"},
				}},
				modelService: &wikiUnavailableModelService{err: buildErr},
				pendingRepo:  pending,
			}
			ctx := types.WithTaskRetryMetadata(context.Background(), test.retried, 3)

			err := svc.ProcessWikiIngest(ctx, asynq.NewTask(types.TypeWikiIngest, payload))

			if test.wantErr {
				require.ErrorIs(t, err, buildErr)
				assert.Empty(t, pending.drained)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{wikiTaskType + "|" + wikiTaskScope + "|kb-1|" + WikiOpIngest}, pending.drained)
		})
	}
}

type panickingKnowledgeService struct {
	interfaces.KnowledgeService
}

func (panickingKnowledgeService) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	panic("unexpected nil row")
}

// errgroup does not recover panics: one escaping a map worker crashed the
// process before the batch settled its claims, and the op hit the same panic
// on every claim. As an error it goes through the fail_count budget instead.
func TestMapOneDocumentRecoveredTurnsPanicIntoError(t *testing.T) {
	svc := &wikiIngestService{knowledgeSvc: panickingKnowledgeService{}}

	result, updates, err := svc.mapOneDocumentRecovered(context.Background(), nil,
		WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
		WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "k-1"}, nil)

	require.ErrorContains(t, err, "panicked")
	assert.Nil(t, result)
	assert.Nil(t, updates)
}
