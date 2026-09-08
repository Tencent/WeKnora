package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReranker struct{ err error }

func (f *fakeReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.RankResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return make([]rerank.RankResult, len(docs)), nil
}
func (f *fakeReranker) GetModelName() string { return "rr-test" }
func (f *fakeReranker) GetModelID() string   { return "rr-id" }

type usageRecorder struct{ rows []*types.ModelUsage }

func (r *usageRecorder) Create(_ context.Context, row *types.ModelUsage) error {
	r.rows = append(r.rows, row)
	return nil
}
func (r *usageRecorder) Summary(context.Context, uint64, interfaces.ModelUsageQuery) (*types.ModelUsageSummary, error) {
	return nil, nil
}

func TestRerankUsageRecordsSuccessAndFailure(t *testing.T) {
	t.Setenv("TOPIC3_MODEL_USAGE_ENABLED", "true")
	recorder := &usageRecorder{}
	wrapped := wrapRerankUsage(&fakeReranker{}, recorder, 7)
	_, err := wrapped.Rerank(context.Background(), "q", []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, recorder.rows, 1)
	assert.Equal(t, "rerank", recorder.rows[0].CallType)
	assert.Equal(t, "success", recorder.rows[0].Status)
	assert.Equal(t, 2, recorder.rows[0].InputCount)

	wantErr := errors.New("provider failed")
	wrapped = wrapRerankUsage(&fakeReranker{err: wantErr}, recorder, 7)
	_, err = wrapped.Rerank(context.Background(), "q", []string{"a"})
	require.ErrorIs(t, err, wantErr)
	require.Len(t, recorder.rows, 2)
	assert.Equal(t, "failed", recorder.rows[1].Status)
	assert.Equal(t, "provider failed", recorder.rows[1].ErrorMessage)
}

func TestEvaluationWorkerCountDefaultsToOne(t *testing.T) {
	t.Setenv("TOPIC3_EVAL_CONCURRENCY", "")
	assert.Equal(t, 1, evaluationWorkerCount())
	t.Setenv("TOPIC3_EVAL_CONCURRENCY", "3")
	assert.Equal(t, 3, evaluationWorkerCount())
}
