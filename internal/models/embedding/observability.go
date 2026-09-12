package embedding

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type observableEmbedder struct{ inner Embedder }

func wrapEmbeddingObservability(inner Embedder) Embedder {
	if inner == nil {
		return nil
	}
	return &observableEmbedder{inner: inner}
}

func (o *observableEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	startedAt := time.Now()
	vector, err := o.inner.Embed(ctx, text)
	o.observe(ctx, startedAt, err)
	return vector, err
}

func (o *observableEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	startedAt := time.Now()
	vectors, err := o.inner.BatchEmbed(ctx, texts)
	o.observe(ctx, startedAt, err)
	return vectors, err
}

func (o *observableEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ Embedder, texts []string,
) ([][]float32, error) {
	startedAt := time.Now()
	// Pass the raw inner model to avoid observing provider-internal sub-batch
	// callbacks twice.
	vectors, err := o.inner.BatchEmbedWithPool(ctx, o.inner, texts)
	o.observe(ctx, startedAt, err)
	return vectors, err
}

func (o *observableEmbedder) observe(ctx context.Context, startedAt time.Time, err error) {
	purpose, _ := types.LLMCallMetadataFromContext(ctx)
	if purpose == "" {
		purpose = "embedding"
	}
	observation := types.LLMCallObservation{
		ModelType: types.ModelTypeEmbedding, ModelID: o.inner.GetModelID(),
		ModelName: o.inner.GetModelName(), Purpose: purpose,
		DurationMS: time.Since(startedAt).Milliseconds(), Success: err == nil,
	}
	if err != nil {
		observation.Error = err.Error()
	}
	types.DispatchLLMCallObservation(ctx, observation)
}

func (o *observableEmbedder) GetModelName() string { return o.inner.GetModelName() }
func (o *observableEmbedder) GetDimensions() int   { return o.inner.GetDimensions() }
func (o *observableEmbedder) GetModelID() string   { return o.inner.GetModelID() }
