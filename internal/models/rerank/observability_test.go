package rerank

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type rerankObservationCollector struct{ calls []types.LLMCallObservation }

func (c *rerankObservationCollector) ObserveLLMCall(call types.LLMCallObservation) {
	c.calls = append(c.calls, call)
}

type observabilityFakeReranker struct{ err error }

func (r *observabilityFakeReranker) Rerank(context.Context, string, []string) ([]RankResult, error) {
	return []RankResult{{Index: 0, RelevanceScore: 1}}, r.err
}
func (r *observabilityFakeReranker) GetModelName() string { return "rerank-model" }
func (r *observabilityFakeReranker) GetModelID() string   { return "rerank-1" }

func TestObservableRerankerRecordsSuccessAndFailure(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "failure", err: errors.New("provider unavailable")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			collector := &rerankObservationCollector{}
			ctx := types.WithLLMCallObserver(context.Background(), collector)
			wrapped := wrapRerankerObservability(&observabilityFakeReranker{err: testCase.err})
			_, err := wrapped.Rerank(ctx, "query", []string{"document"})
			require.ErrorIs(t, err, testCase.err)
			require.Len(t, collector.calls, 1)
			require.Equal(t, types.ModelTypeRerank, collector.calls[0].ModelType)
			require.Equal(t, testCase.err == nil, collector.calls[0].Success)
		})
	}
}
