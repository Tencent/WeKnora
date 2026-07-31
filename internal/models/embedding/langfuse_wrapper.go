package embedding

import (
	"context"
	"errors"

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
	genCtx, gen := mgr.StartGeneration(
		ctx,
		buildLangfuseEmbeddingOptions(
			ctx,
			"embedding.embed",
			l.inner.GetModelName(),
			l.inner.GetModelID(),
			[]string{text},
			l.inner.GetDimensions(),
			false,
		),
	)
	result, err := l.inner.Embed(genCtx, text)
	usage := approxEmbeddingUsage([]string{text})
	gen.Finish(
		buildLangfuseEmbeddingOutput(ctx, singleToDouble(result)),
		usage,
		buildLangfuseEmbeddingError(ctx, err),
	)
	return result, err
}

func (l *langfuseEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.BatchEmbed(ctx, texts)
	}
	if _, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		genCtx, gen := mgr.StartGeneration(
			ctx,
			buildLangfuseEmbeddingOptions(
				ctx,
				"embedding.batch_embed",
				l.inner.GetModelName(),
				l.inner.GetModelID(),
				texts,
				l.inner.GetDimensions(),
				true,
			),
		)
		result, err := l.inner.BatchEmbed(genCtx, texts)
		gen.Finish(
			buildLangfuseEmbeddingOutput(ctx, result),
			approxEmbeddingUsage(texts),
			buildLangfuseEmbeddingError(ctx, err),
		)
		return result, err
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

func buildLangfuseEmbeddingOptions(
	ctx context.Context,
	name string,
	modelName string,
	modelID string,
	texts []string,
	dimensions int,
	batch bool,
) langfuse.GenerationOptions {
	var input interface{}
	if batch {
		input = map[string]interface{}{
			"count":   len(texts),
			"preview": previewTexts(texts, 5),
		}
	} else if len(texts) > 0 {
		input = texts[0]
	}
	metadata := map[string]interface{}{
		"model_id":   modelID,
		"dimensions": dimensions,
	}
	if batch {
		metadata["batch_size"] = len(texts)
	}
	options := langfuse.GenerationOptions{
		Name:     name,
		Model:    modelName,
		Input:    input,
		Metadata: metadata,
	}
	if hashPrefix, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		totalLength := 0
		for _, text := range texts {
			totalLength += len([]rune(text))
		}
		options.Model = "prepared-knowledge-model"
		options.Input = map[string]interface{}{
			"input_count":       len(texts),
			"input_length":      totalLength,
			"scope_hash_prefix": hashPrefix,
		}
		options.Metadata = map[string]interface{}{
			"dimensions":        dimensions,
			"batch_size":        len(texts),
			"scope_hash_prefix": hashPrefix,
		}
	}
	return options
}

func buildLangfuseEmbeddingOutput(
	ctx context.Context,
	result [][]float32,
) interface{} {
	if hashPrefix, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		dimensions := 0
		if len(result) > 0 {
			dimensions = len(result[0])
		}
		return map[string]interface{}{
			"count":             len(result),
			"dimensions":        dimensions,
			"scope_hash_prefix": hashPrefix,
		}
	}
	if len(result) == 0 {
		return nil
	}
	if len(result) == 1 {
		return map[string]interface{}{
			"dimensions":     len(result[0]),
			"vector_preview": result[0][:min(3, len(result[0]))],
		}
	}
	return map[string]interface{}{
		"count":      len(result),
		"dimensions": len(result[0]),
	}
}

func buildLangfuseEmbeddingError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if _, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		return errors.New("prepared embedding failed")
	}
	return err
}

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
