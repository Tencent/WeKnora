package embedding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/observe"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// debugEmbedder + langfuseEmbedder used to live in llm_debug.go + langfuse_wrapper.go
// as two near-identical decorators. They now share the observe.Wrap helper.
// The Embedder interface has two methods (Embed, BatchEmbed) plus the pooler
// pass-through, so we define one wrapper that covers both.

type debugEmbedder struct {
	inner Embedder
}

func (d *debugEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return observe.Wrap(ctx, embedCall(d.inner, "embedding.embed"), text, d.inner.Embed)
}

func (d *debugEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return observe.Wrap(ctx, batchEmbedCall(d.inner, "embedding.batch_embed"), texts, d.inner.BatchEmbed)
}

func (d *debugEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	// Pooled batching shells out to BatchEmbed via the pool; we pass `d` as
	// the inner so the pool sees the instrumented methods.
	return d.inner.BatchEmbedWithPool(ctx, d, texts)
}

func (d *debugEmbedder) GetModelName() string { return d.inner.GetModelName() }
func (d *debugEmbedder) GetDimensions() int   { return d.inner.GetDimensions() }
func (d *debugEmbedder) GetModelID() string   { return d.inner.GetModelID() }

// langfuseEmbedder historically was a separate decorator placed outside of
// debugEmbedder in NewEmbedder. We keep that layering so enabling/disabling
// each subsystem independently still works, but both now route through the
// same observe.Wrap path internally.
type langfuseEmbedder struct {
	inner Embedder
}

func (l *langfuseEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return observe.Wrap(ctx, embedCall(l.inner, "embedding.embed"), text, l.inner.Embed)
}

func (l *langfuseEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return observe.Wrap(ctx, batchEmbedCall(l.inner, "embedding.batch_embed"), texts, l.inner.BatchEmbed)
}

func (l *langfuseEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	return l.inner.BatchEmbedWithPool(ctx, l, texts)
}

func (l *langfuseEmbedder) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseEmbedder) GetDimensions() int   { return l.inner.GetDimensions() }
func (l *langfuseEmbedder) GetModelID() string   { return l.inner.GetModelID() }

// embedCall builds the observe spec for the single-text Embed method.
func embedCall(inner Embedder, name string) observe.Call[string, []float32] {
	return observe.Call[string, []float32]{
		Name:    name,
		Model:   inner.GetModelName(),
		ModelID: inner.GetModelID(),
		LangfuseInput: func(text string) any {
			return text
		},
		LangfuseMetadata: func(_ string) map[string]any {
			return map[string]any{
				"model_id":   inner.GetModelID(),
				"dimensions": inner.GetDimensions(),
			}
		},
		LangfuseOutput: func(_ string, result []float32, _ error) any {
			if len(result) == 0 {
				return nil
			}
			preview := result
			if len(preview) > 3 {
				preview = preview[:3]
			}
			return map[string]any{
				"dimensions":     len(result),
				"vector_preview": preview,
			}
		},
		Usage: func(text string, _ []float32) *langfuse.TokenUsage {
			return approxEmbeddingUsage([]string{text})
		},
		DebugRecord: func(text string, result []float32, callErr error, dur time.Duration) *logger.LLMCallRecord {
			return buildEmbeddingDebugRecord(inner.GetModelName(), []string{text}, singleToDouble(result), callErr, dur)
		},
	}
}

// batchEmbedCall builds the observe spec for BatchEmbed.
func batchEmbedCall(inner Embedder, name string) observe.Call[[]string, [][]float32] {
	return observe.Call[[]string, [][]float32]{
		Name:    name,
		Model:   inner.GetModelName(),
		ModelID: inner.GetModelID(),
		LangfuseInput: func(texts []string) any {
			return map[string]any{
				"count":   len(texts),
				"preview": previewTexts(texts, 5),
			}
		},
		LangfuseMetadata: func(texts []string) map[string]any {
			return map[string]any{
				"model_id":   inner.GetModelID(),
				"dimensions": inner.GetDimensions(),
				"batch_size": len(texts),
			}
		},
		LangfuseOutput: func(_ []string, result [][]float32, _ error) any {
			if len(result) == 0 {
				return nil
			}
			dims := 0
			if len(result[0]) > 0 {
				dims = len(result[0])
			}
			return map[string]any{
				"count":      len(result),
				"dimensions": dims,
			}
		},
		Usage: func(texts []string, _ [][]float32) *langfuse.TokenUsage {
			return approxEmbeddingUsage(texts)
		},
		DebugRecord: func(texts []string, result [][]float32, callErr error, dur time.Duration) *logger.LLMCallRecord {
			return buildEmbeddingDebugRecord(inner.GetModelName(), texts, result, callErr, dur)
		},
	}
}

// --- helpers previously split across llm_debug.go and langfuse_wrapper.go ---

func singleToDouble(v []float32) [][]float32 {
	if v == nil {
		return nil
	}
	return [][]float32{v}
}

func buildEmbeddingDebugRecord(model string, inputs []string, outputs [][]float32, callErr error, dur time.Duration) *logger.LLMCallRecord {
	record := &logger.LLMCallRecord{
		CallType: "Embedding",
		Model:    model,
		Duration: dur,
	}

	// Input section
	var inputBuf strings.Builder
	fmt.Fprintf(&inputBuf, "count=%d\n", len(inputs))
	for i, t := range inputs {
		preview := strings.ReplaceAll(t, "\n", "\\n")
		preview = logger.TruncateRunes(preview, 200)
		fmt.Fprintf(&inputBuf, "[%d] (len=%d) %s\n", i, len([]rune(t)), preview)
	}
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Input", Content: inputBuf.String()})

	// Output section
	if outputs != nil {
		var outBuf strings.Builder
		fmt.Fprintf(&outBuf, "count=%d\n", len(outputs))
		for i, vec := range outputs {
			if len(vec) > 0 {
				fmt.Fprintf(&outBuf, "[%d] dims=%d, first_3=[%.6f, %.6f, %.6f]\n", i, len(vec),
					safeIdx(vec, 0), safeIdx(vec, 1), safeIdx(vec, 2))
			} else {
				fmt.Fprintf(&outBuf, "[%d] empty\n", i)
			}
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Output", Content: outBuf.String()})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	return record
}

func safeIdx(v []float32, i int) float32 {
	if i < len(v) {
		return v[i]
	}
	return 0
}

// approxEmbeddingUsage estimates input tokens as ~rune_count / 4. Purely for
// Langfuse cost reporting — users configure per-model cost multipliers.
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
