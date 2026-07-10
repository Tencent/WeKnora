package embedding

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
)

type quotaEmbedder struct {
	inner  Embedder
	key    string
	limits limiter.Limits
}

func (w *quotaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return w.run(ctx, []string{text}, func(callCtx context.Context) ([]float32, error) {
		return w.inner.Embed(callCtx, text)
	})
}

func (w *quotaEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	estimate := estimateEmbeddingUsage(texts, w.inner.GetModelName()).TotalTokens
	permit, err := limiter.Admit(ctx, w.key, w.limits, estimate)
	if err != nil {
		return nil, err
	}
	usageCtx, capture := WithUsageCapture(ctx)
	result, err := w.inner.BatchEmbed(usageCtx, texts)
	permit.Complete(capture.Usage().TotalTokens)
	return result, err
}

func (w *quotaEmbedder) run(ctx context.Context, texts []string, call func(context.Context) ([]float32, error)) ([]float32, error) {
	estimate := estimateEmbeddingUsage(texts, w.inner.GetModelName()).TotalTokens
	permit, err := limiter.Admit(ctx, w.key, w.limits, estimate)
	if err != nil {
		return nil, err
	}
	usageCtx, capture := WithUsageCapture(ctx)
	result, err := call(usageCtx)
	permit.Complete(capture.Usage().TotalTokens)
	return result, err
}

func (w *quotaEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return w.inner.BatchEmbedWithPool(ctx, w, texts)
}

func (w *quotaEmbedder) GetModelName() string { return w.inner.GetModelName() }
func (w *quotaEmbedder) GetDimensions() int   { return w.inner.GetDimensions() }
func (w *quotaEmbedder) GetModelID() string   { return w.inner.GetModelID() }

func wrapEmbeddingConcurrency(e Embedder, config Config) Embedder {
	if e == nil {
		return e
	}
	return &quotaEmbedder{
		inner: e,
		key:   limiter.QuotaKey(config.TenantID, e.GetModelID(), config.QuotaGroup),
		limits: limiter.Limits{
			MaxConcurrency:                config.MaxConcurrency,
			RequestsPerMinute:             config.RequestsPerMinute,
			TokensPerMinute:               config.TokensPerMinute,
			InteractiveConcurrencyReserve: config.InteractiveConcurrencyReserve,
		},
	}
}
