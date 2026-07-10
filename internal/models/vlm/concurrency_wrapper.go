package vlm

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
)

// Multimodal enrichment (image OCR / caption) is a high-volume, slow background
// stage that hits the same provider budget as chat. Like chat, it must be
// governed at the client layer so an image-heavy ingestion storm can't burst
// the whole worker pool — or exceed the provider's RPM/TPM quota — against one
// VLM provider. Three dimensions are enforced together (see limiter.Limits):
// concurrency, RPM and TPM. Only background (asynq worker) calls are throttled
// — see types.IsBackgroundTask.
//
// TPM note: VLM APIs return no token usage here, so the reservation is sized
// from the prompt estimate plus a flat per-image token allowance and NOT
// reconciled afterwards (Release is passed -1).
type concurrencyVLM struct {
	inner VLM
	// limits are this model's configured per-model background caps; a 0 in any
	// dimension falls back to the process-wide default (see limiter.Admit).
	limits limiter.Limits
}

// perImageTokens is a coarse allowance for one image's contribution to the TPM
// budget. Image token cost varies wildly by provider and resolution; this is a
// deliberately rough middle-ground so image-heavy calls reserve materially more
// than text-only ones without needing provider-specific tiling math.
const perImageTokens = 1000

func (w *concurrencyVLM) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyVLM) GetModelID() string   { return w.inner.GetModelID() }

func (w *concurrencyVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	est := limiter.EstimateTokens(prompt) + len(imgBytes)*perImageTokens
	res := limiter.Admit(ctx, w.inner.GetModelID(), w.limits, est)
	defer res.Release(-1)
	return w.inner.Predict(ctx, imgBytes, prompt)
}

// wrapVLMConcurrency installs the background governor as the outermost VLM
// decorator. Always applied; a cheap passthrough when no limiter is installed
// or the call is interactive.
func wrapVLMConcurrency(v VLM, limits limiter.Limits, err error) (VLM, error) {
	if err != nil || v == nil {
		return v, err
	}
	return &concurrencyVLM{inner: v, limits: limits}, nil
}
