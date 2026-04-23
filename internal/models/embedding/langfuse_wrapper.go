package embedding

import (
	"context"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// langfuseEmbedder wraps an Embedder and reports each call as a Langfuse
// generation observation. Input token counts are approximated from the text
// lengths when the underlying provider doesn't return usage data, because
// Langfuse's cost reports require non-zero input tokens.
type langfuseEmbedder struct {
	inner Embedder
}

func (l *langfuseEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.Embed(ctx, text)
	}
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "embedding.embed",
		Model: l.inner.GetModelName(),
		Input: text,
		Metadata: map[string]interface{}{
			"model_id":   l.inner.GetModelID(),
			"dimensions": l.inner.GetDimensions(),
		},
	})
	result, err := l.inner.Embed(genCtx, text)
	usage := approxEmbeddingUsage([]string{text})
	var out interface{}
	if len(result) > 0 {
		out = map[string]interface{}{
			"dimensions":     len(result),
			"vector_preview": result[:min(3, len(result))],
		}
	}
	gen.Finish(out, usage, err)
	return result, err
}

func (l *langfuseEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.BatchEmbed(ctx, texts)
	}
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "embedding.batch_embed",
		Model: l.inner.GetModelName(),
		Input: map[string]interface{}{
			"count": len(texts),
			// Avoid sending megabytes of full text — Langfuse truncates but
			// the network cost is still real. Keep a short preview instead.
			"preview": previewTexts(texts, 5),
		},
		Metadata: map[string]interface{}{
			"model_id":   l.inner.GetModelID(),
			"dimensions": l.inner.GetDimensions(),
			"batch_size": len(texts),
		},
	})
	result, err := l.inner.BatchEmbed(genCtx, texts)
	usage := approxEmbeddingUsage(texts)
	var out interface{}
	if len(result) > 0 {
		out = map[string]interface{}{
			"count":      len(result),
			"dimensions": len(result[0]),
		}
	}
	gen.Finish(out, usage, err)
	return result, err
}

func (l *langfuseEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return l.inner.BatchEmbedWithPool(ctx, l, texts)
}

func (l *langfuseEmbedder) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseEmbedder) GetDimensions() int   { return l.inner.GetDimensions() }
func (l *langfuseEmbedder) GetModelID() string   { return l.inner.GetModelID() }

// approxEmbeddingUsage estimates input tokens as ~rune_count / 4, matching the
// rule of thumb OpenAI uses in their tokenizer docs. This is purely for cost
// reporting — Langfuse lets users define per-model cost multipliers, so the
// approximation need only be proportional to length.
func approxEmbeddingUsage(texts []string) *langfuse.TokenUsage {
	total := 0
	for _, t := range texts {
		runes := len([]rune(t))
		if runes == 0 {
			continue
		}
		total += runes/4 + 1
	}
	if total == 0 {
		return nil
	}
	return &langfuse.TokenUsage{
		Input: total,
		Total: total,
		Unit:  "TOKENS",
	}
}

func previewTexts(texts []string, n int) []string {
	if len(texts) <= n {
		out := make([]string, len(texts))
		for i, t := range texts {
			out[i] = truncateRunes(t, 120)
		}
		return out
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = truncateRunes(texts[i], 120)
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
