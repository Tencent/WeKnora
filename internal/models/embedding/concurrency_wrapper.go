package embedding

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
)

// Embedding is the highest-volume background model call: document ingestion
// vectorises every chunk, so a single batch upload can burst the whole worker
// pool against one embedding provider — and, for short chunks, blow past its
// requests-per-minute quota long before concurrency alone would bite. Like chat
// and vlm, embedding is governed at the client layer via the shared per-model
// governor across three dimensions: concurrency, RPM and TPM (see
// limiter.Limits). Only background (asynq worker) calls are throttled — see
// types.IsBackgroundTask; interactive query embedding is never gated.
//
// TPM note: embedding APIs return no token usage, so the per-call reservation
// is sized from the input text estimate and NOT reconciled afterwards (Release
// is passed -1). Input-only estimation is fairly accurate for embedding.
//
// Placement note: unlike chat/vlm (outermost), this wrapper sits INNERMOST —
// directly around the real embedder, BELOW the debug/langfuse decorators.
// BatchEmbedWithPool fans a batch out into per-sub-batch BatchEmbed calls
// through the pooler, and the pooler invokes BatchEmbed on whichever Embedder
// was threaded down as `model`. Sitting innermost is what routes those
// per-sub-batch provider round-trips back through the governor, so the limits
// bound real concurrent provider calls rather than one coarse per-document
// unit. The trade-off is that the wait time is included in debug/langfuse
// timing, which is acceptable for background ingestion.
type concurrencyEmbedder struct {
	inner Embedder
	// limits are this model's configured per-model background caps; a 0 in any
	// dimension falls back to the process-wide default (see limiter.Admit).
	limits limiter.Limits
}

func (w *concurrencyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	res := limiter.Admit(ctx, w.inner.GetModelID(), w.limits, limiter.EstimateTokens(text))
	defer res.Release(-1)
	return w.inner.Embed(ctx, text)
}

func (w *concurrencyEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	est := 0
	for _, t := range texts {
		est += limiter.EstimateTokens(t)
	}
	res := limiter.Admit(ctx, w.inner.GetModelID(), w.limits, est)
	defer res.Release(-1)
	return w.inner.BatchEmbed(ctx, texts)
}

// BatchEmbedWithPool threads THIS wrapper down as the model so the pooler's
// per-sub-batch callbacks land on our gated BatchEmbed above, rather than on
// the raw embedder. The reservation for each sub-batch is held only around the
// actual per-sub-batch provider round-trip.
func (w *concurrencyEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	return w.inner.BatchEmbedWithPool(ctx, w, texts)
}

func (w *concurrencyEmbedder) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyEmbedder) GetDimensions() int   { return w.inner.GetDimensions() }
func (w *concurrencyEmbedder) GetModelID() string   { return w.inner.GetModelID() }

// wrapEmbeddingConcurrency installs the background governor directly around the
// real embedder. Always applied; a cheap passthrough when no limiter is
// installed or the call is interactive.
func wrapEmbeddingConcurrency(e Embedder, limits limiter.Limits) Embedder {
	if e == nil {
		return e
	}
	return &concurrencyEmbedder{inner: e, limits: limits}
}
