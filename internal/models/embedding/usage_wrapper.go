package embedding

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/modelusage"
	"github.com/Tencent/WeKnora/internal/types"
)

// usageEmbedder records every embed call as a model usage event. Embedders
// don't return token usage, so input tokens are estimated (~runes/4, same
// approximation as the langfuse wrapper) and flagged as estimated.
type usageEmbedder struct {
	inner    Embedder
	source   types.ModelSource
	provider string
}

func (u *usageEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	result, err := u.inner.Embed(ctx, text)
	u.record(ctx, "embedding.embed", []string{text}, 1, time.Since(start), err == nil, modelusage.ErrorType(err))
	return result, err
}

func (u *usageEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	result, err := u.inner.BatchEmbed(ctx, texts)
	u.record(ctx, "embedding.batch_embed", texts, len(texts), time.Since(start), err == nil, modelusage.ErrorType(err))
	return result, err
}

func (u *usageEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	return u.inner.BatchEmbedWithPool(ctx, u, texts)
}

func (u *usageEmbedder) GetModelName() string { return u.inner.GetModelName() }
func (u *usageEmbedder) GetDimensions() int   { return u.inner.GetDimensions() }
func (u *usageEmbedder) GetModelID() string   { return u.inner.GetModelID() }

func (u *usageEmbedder) record(
	ctx context.Context,
	kind string,
	texts []string,
	inputItems int,
	duration time.Duration,
	success bool,
	errorType string,
) {
	event := types.ModelUsageEvent{
		ModelID:     u.inner.GetModelID(),
		ModelName:   u.inner.GetModelName(),
		ModelType:   types.ModelTypeEmbedding,
		ModelSource: u.source,
		Provider:    u.provider,
		RequestKind: kind,
		UsageSource: types.ModelUsageSourceEstimated,
		InputItems:  inputItems,
		DurationMs:  duration.Milliseconds(),
		Success:     success,
		ErrorType:   errorType,
	}
	if usage := approxEmbeddingUsage(texts); usage != nil {
		event.PromptTokens = int64(usage.Input)
		event.TotalTokens = int64(usage.Total)
	} else {
		event.UsageSource = types.ModelUsageSourceMissing
	}
	modelusage.Record(ctx, event)
}

// wrapEmbedderUsage applies the usage-recording decorator.
func wrapEmbedderUsage(e Embedder, config Config) Embedder {
	if e == nil {
		return e
	}
	return &usageEmbedder{inner: e, source: config.Source, provider: config.Provider}
}
