package embedding

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
)

// Embedding is the highest-volume background model call: document ingestion
// vectorises every chunk, so a single batch upload can burst the whole worker
// pool against one embedding provider. Like chat and vlm, embedding is governed
// at the client layer via the shared per-model concurrency governor. Only
// background (asynq worker) calls are throttled — see limiter.Gate /
// types.IsBackgroundTask; interactive query embedding is never gated.
//
// Placement note: unlike chat/vlm (outermost), this wrapper sits INNERMOST —
// directly around the real embedder, BELOW the debug/langfuse decorators.
// BatchEmbedWithPool fans a batch out into per-sub-batch BatchEmbed calls
// through the pooler, and the pooler invokes BatchEmbed on whichever Embedder
// was threaded down as `model`. Sitting innermost is what routes those
// per-sub-batch provider round-trips back through Gate, so the semaphore bounds
// real concurrent provider calls rather than one coarse per-document unit. The
// trade-off is that Gate wait time is included in debug/langfuse timing, which
// is acceptable for background ingestion.
type concurrencyEmbedder struct {
	inner Embedder
	// limit is this model's configured per-model background cap; 0 falls back
	// to the process-wide default (see limiter.GateN).
	limit int
}

func (w *concurrencyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	release := limiter.GateNamedN(ctx, w.inner.GetModelID(), w.inner.GetModelName(), w.limit)
	defer release()
	return w.inner.Embed(ctx, text)
}

func (w *concurrencyEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	release := limiter.GateNamedN(ctx, w.inner.GetModelID(), w.inner.GetModelName(), w.limit)
	defer release()
	return w.inner.BatchEmbed(ctx, texts)
}

// BatchEmbedWithPool threads THIS wrapper down as the model so the pooler's
// per-sub-batch callbacks land on our gated BatchEmbed above, rather than on
// the raw embedder. The wait for each slot is held only around the actual
// per-sub-batch provider round-trip.
func (w *concurrencyEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	return w.inner.BatchEmbedWithPool(ctx, w, texts)
}

func (w *concurrencyEmbedder) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyEmbedder) GetDimensions() int   { return w.inner.GetDimensions() }
func (w *concurrencyEmbedder) GetModelID() string   { return w.inner.GetModelID() }

// wrapEmbeddingConcurrency installs the background concurrency governor directly
// around the real embedder. Always applied; a cheap passthrough when no limiter
// is installed or the call is interactive.
func wrapEmbeddingConcurrency(e Embedder, limit int) Embedder {
	if e == nil {
		return e
	}
	return &concurrencyEmbedder{inner: e, limit: limit}
}
