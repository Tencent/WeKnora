package vlm

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
)

// Multimodal enrichment (image OCR / caption) is a high-volume, slow background
// stage that hits the same provider budget as chat. Like chat, it must be
// governed at the client layer so an image-heavy ingestion storm can't burst
// the whole worker pool against one VLM provider. Only background (asynq
// worker) calls are throttled — see limiter.Gate / types.IsBackgroundTask.
type concurrencyVLM struct {
	inner VLM
	// limit is this model's configured per-model background cap; 0 falls back
	// to the process-wide default (see limiter.GateN).
	limit int
}

func (w *concurrencyVLM) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyVLM) GetModelID() string   { return w.inner.GetModelID() }

func (w *concurrencyVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	release := limiter.GateNamedN(ctx, w.inner.GetModelID(), w.inner.GetModelName(), w.limit)
	defer release()
	return w.inner.Predict(ctx, imgBytes, prompt)
}

// wrapVLMConcurrency installs the background concurrency governor as the
// outermost VLM decorator. Always applied; a cheap passthrough when no limiter
// is installed or the call is interactive.
func wrapVLMConcurrency(v VLM, limit int, err error) (VLM, error) {
	if err != nil || v == nil {
		return v, err
	}
	return &concurrencyVLM{inner: v, limit: limit}, nil
}
