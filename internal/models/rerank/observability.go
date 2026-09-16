package rerank

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type observableReranker struct{ inner Reranker }

func wrapRerankerObservability(inner Reranker) Reranker {
	if inner == nil {
		return nil
	}
	return &observableReranker{inner: inner}
}

func (o *observableReranker) Rerank(
	ctx context.Context, query string, documents []string,
) ([]RankResult, error) {
	startedAt := time.Now()
	results, err := o.inner.Rerank(ctx, query, documents)
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	if purpose == "" {
		purpose = "rerank"
	}
	observation := types.LLMCallObservation{
		ModelType: types.ModelTypeRerank, ModelID: o.inner.GetModelID(),
		ModelName: o.inner.GetModelName(), Purpose: purpose,
		DurationMS: time.Since(startedAt).Milliseconds(), Success: err == nil,
	}
	if err != nil {
		observation.Error = err.Error()
	}
	types.DispatchLLMCallObservation(ctx, observation)
	return results, err
}

func (o *observableReranker) GetModelName() string { return o.inner.GetModelName() }
func (o *observableReranker) GetModelID() string   { return o.inner.GetModelID() }
