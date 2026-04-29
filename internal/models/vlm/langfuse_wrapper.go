package vlm

import (
	"context"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// langfuseVLM wraps a VLM and reports each Predict call as a Langfuse
// generation. The raw image bytes are NOT uploaded — Langfuse traces are
// designed for text. We include image count and total byte size in the
// metadata, plus the text prompt, which matches how Langfuse's own VLM
// integrations report multimodal calls.
type langfuseVLM struct {
	inner VLM
}

func (l *langfuseVLM) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseVLM) GetModelID() string   { return l.inner.GetModelID() }

func (l *langfuseVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.Predict(ctx, imgBytes, prompt)
	}

	totalImgSize := 0
	for _, b := range imgBytes {
		totalImgSize += len(b)
	}

	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "vlm.predict",
		Model: l.inner.GetModelName(),
		Input: map[string]interface{}{
			"prompt":      prompt,
			"image_count": len(imgBytes),
		},
		Metadata: map[string]interface{}{
			"model_id":         l.inner.GetModelID(),
			"image_count":      len(imgBytes),
			"image_bytes_total": totalImgSize,
		},
	})

	result, err := l.inner.Predict(genCtx, imgBytes, prompt)

	// VLMs don't return token usage; approximate prompt tokens for cost
	// tracking in Langfuse (users can configure per-model pricing in the UI).
	promptTokens := len([]rune(prompt))/4 + 1
	outputTokens := len([]rune(result)) / 4
	usage := &langfuse.TokenUsage{
		Input:  promptTokens,
		Output: outputTokens,
		Total:  promptTokens + outputTokens,
		Unit:   "TOKENS",
	}

	gen.Finish(result, usage, err)
	return result, err
}

// wrapVLMLangfuse applies the Langfuse decorator when the manager is enabled.
func wrapVLMLangfuse(v VLM, err error) (VLM, error) {
	if err != nil || v == nil {
		return v, err
	}
	if !langfuse.GetManager().Enabled() {
		return v, nil
	}
	return &langfuseVLM{inner: v}, nil
}
