package embedding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// debugEmbedder wraps an Embedder with LLM debug logging.
type debugEmbedder struct {
	inner Embedder
}

func (d *debugEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	result, err := d.inner.Embed(ctx, text)
	logEmbeddingDebug(ctx, d.inner.GetModelName(), []string{text}, singleToDouble(result), err, time.Since(start))
	return result, err
}

func (d *debugEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	result, err := d.inner.BatchEmbed(ctx, texts)
	logEmbeddingDebug(ctx, d.inner.GetModelName(), texts, result, err, time.Since(start))
	return result, err
}

func (d *debugEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return d.inner.BatchEmbedWithPool(ctx, d, texts)
}

// BatchEmbedMultimodal forwards the call and records it in the LLM debug log.
// Like the text paths it is unconditional, so the capability survives wrapping
// while Capabilities stays truthful about the inner model.
func (d *debugEmbedder) BatchEmbedMultimodal(
	ctx context.Context, inputs []Input,
) ([][]float32, error) {
	start := time.Now()
	result, err := BatchEmbedMultimodalWith(ctx, d.inner, inputs)
	logEmbeddingDebug(ctx, d.inner.GetModelName(), debugInputPreview(inputs), result, err, time.Since(start))
	return result, err
}

func (d *debugEmbedder) Capabilities() Capabilities {
	return CapabilitiesOf(d.inner)
}

// debugInputPreview renders multimodal inputs as short text lines. Image
// payloads are never printed in full — a single page image is megabytes of
// base64 and would swamp the debug log.
func debugInputPreview(inputs []Input) []string {
	out := make([]string, 0, len(inputs))
	for _, in := range inputs {
		var sb strings.Builder
		if text := strings.TrimSpace(in.Text); text != "" {
			sb.WriteString(logger.TruncateRunes(strings.ReplaceAll(text, "\n", "\\n"), 120))
		}
		if len(in.Images) > 0 {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			fmt.Fprintf(&sb, "[%d image(s)]", len(in.Images))
		}
		out = append(out, sb.String())
	}
	return out
}

func (d *debugEmbedder) GetModelName() string { return d.inner.GetModelName() }
func (d *debugEmbedder) GetDimensions() int   { return d.inner.GetDimensions() }
func (d *debugEmbedder) GetModelID() string   { return d.inner.GetModelID() }

func singleToDouble(v []float32) [][]float32 {
	if v == nil {
		return nil
	}
	return [][]float32{v}
}

func logEmbeddingDebug(ctx context.Context, model string, inputs []string, outputs [][]float32, callErr error, dur time.Duration) {
	if !logger.LLMDebugEnabled() {
		return
	}

	record := &logger.LLMCallRecord{
		CallType: "Embedding",
		Model:    model,
		Duration: dur,
	}

	// Input section: show each text with a preview
	var inputBuf strings.Builder
	inputBuf.WriteString(fmt.Sprintf("count=%d\n", len(inputs)))
	for i, t := range inputs {
		preview := strings.ReplaceAll(t, "\n", "\\n")
		preview = logger.TruncateRunes(preview, 200)
		inputBuf.WriteString(fmt.Sprintf("[%d] (len=%d) %s\n", i, len([]rune(t)), preview))
	}
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Input", Content: inputBuf.String()})

	// Output section
	if outputs != nil {
		var outBuf strings.Builder
		outBuf.WriteString(fmt.Sprintf("count=%d\n", len(outputs)))
		for i, vec := range outputs {
			if len(vec) > 0 {
				outBuf.WriteString(fmt.Sprintf("[%d] dims=%d, first_3=[%.6f, %.6f, %.6f]\n", i, len(vec),
					safeIdx(vec, 0), safeIdx(vec, 1), safeIdx(vec, 2)))
			} else {
				outBuf.WriteString(fmt.Sprintf("[%d] empty\n", i))
			}
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Output", Content: outBuf.String()})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	logger.LLMDebugLog(ctx, record)
}

func safeIdx(v []float32, i int) float32 {
	if i < len(v) {
		return v[i]
	}
	return 0
}
