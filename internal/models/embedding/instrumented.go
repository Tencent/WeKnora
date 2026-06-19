package embedding

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/observability"
)

const defaultEmbeddingCallTimeout = 60 * time.Second

type instrumentedEmbedder struct {
	inner    Embedder
	provider string
	timeout  time.Duration
}

func newInstrumentedEmbedder(inner Embedder, provider string, timeout time.Duration) Embedder {
	if inner == nil {
		return inner
	}
	if timeout <= 0 {
		timeout = defaultEmbeddingCallTimeout
	}
	return &instrumentedEmbedder{inner: inner, provider: provider, timeout: timeout}
}

func (i *instrumentedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	callCtx, cancel := contextWithFallbackTimeout(ctx, i.timeout)
	defer cancel()
	done := observability.ModelCallStarted(i.provider, i.inner.GetModelName())
	result, err := i.inner.Embed(callCtx, text)
	done(err)
	return result, err
}

func (i *instrumentedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	callCtx, cancel := contextWithFallbackTimeout(ctx, i.timeout)
	defer cancel()
	done := observability.ModelCallStarted(i.provider, i.inner.GetModelName())
	result, err := i.inner.BatchEmbed(callCtx, texts)
	done(err)
	return result, err
}

func (i *instrumentedEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	callCtx, cancel := contextWithFallbackTimeout(ctx, i.timeout)
	defer cancel()
	return i.inner.BatchEmbedWithPool(callCtx, model, texts)
}

func (i *instrumentedEmbedder) GetModelName() string { return i.inner.GetModelName() }
func (i *instrumentedEmbedder) GetDimensions() int   { return i.inner.GetDimensions() }
func (i *instrumentedEmbedder) GetModelID() string   { return i.inner.GetModelID() }

func contextWithFallbackTimeout(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	if fallback <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= fallback {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, fallback)
}
