package rerank

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/modelusage"
	"github.com/Tencent/WeKnora/internal/types"
)

// usageReranker records every Rerank call as a model usage event. Rerankers
// don't return token usage, so input tokens are estimated (same approximation
// as the langfuse wrapper) and flagged as estimated.
type usageReranker struct {
	inner    Reranker
	source   types.ModelSource
	provider string
}

func (u *usageReranker) GetModelName() string { return u.inner.GetModelName() }
func (u *usageReranker) GetModelID() string   { return u.inner.GetModelID() }

func (u *usageReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	start := time.Now()
	results, err := u.inner.Rerank(ctx, query, documents)
	event := types.ModelUsageEvent{
		ModelID:     u.inner.GetModelID(),
		ModelName:   u.inner.GetModelName(),
		ModelType:   types.ModelTypeRerank,
		ModelSource: u.source,
		Provider:    u.provider,
		RequestKind: "rerank",
		UsageSource: types.ModelUsageSourceEstimated,
		InputItems:  len(documents),
		DurationMs:  time.Since(start).Milliseconds(),
		Success:     err == nil,
		ErrorType:   modelusage.ErrorType(err),
	}
	if usage := approxRerankUsage(query, documents); usage != nil {
		event.PromptTokens = int64(usage.Input)
		event.TotalTokens = int64(usage.Total)
	} else {
		event.UsageSource = types.ModelUsageSourceMissing
	}
	modelusage.Record(ctx, event)
	return results, err
}

// wrapRerankerUsage applies the usage-recording decorator.
func wrapRerankerUsage(r Reranker, config *RerankerConfig) Reranker {
	if r == nil {
		return r
	}
	source := types.ModelSource("")
	provider := ""
	if config != nil {
		source = config.Source
		provider = config.Provider
	}
	return &usageReranker{inner: r, source: source, provider: provider}
}
