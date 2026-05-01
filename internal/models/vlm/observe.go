package vlm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/observe"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// Unified debug/langfuse decorator for VLM. Replaces two near-identical files
// (llm_debug.go + langfuse_wrapper.go) with one observe.Wrap-based flow.

type vlmInput struct {
	imgBytes [][]byte
	prompt   string
}

type debugVLM struct {
	inner VLM
}

func (d *debugVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	return runVLMWrapped(ctx, d.inner, imgBytes, prompt)
}
func (d *debugVLM) GetModelName() string { return d.inner.GetModelName() }
func (d *debugVLM) GetModelID() string   { return d.inner.GetModelID() }

type langfuseVLM struct {
	inner VLM
}

func (l *langfuseVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	return runVLMWrapped(ctx, l.inner, imgBytes, prompt)
}
func (l *langfuseVLM) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseVLM) GetModelID() string   { return l.inner.GetModelID() }

func runVLMWrapped(ctx context.Context, inner VLM, imgBytes [][]byte, prompt string) (string, error) {
	in := vlmInput{imgBytes: imgBytes, prompt: prompt}
	call := observe.Call[vlmInput, string]{
		Name:    "vlm.predict",
		Model:   inner.GetModelName(),
		ModelID: inner.GetModelID(),
		LangfuseInput: func(v vlmInput) any {
			return map[string]any{
				"prompt":      v.prompt,
				"image_count": len(v.imgBytes),
			}
		},
		LangfuseMetadata: func(v vlmInput) map[string]any {
			total := 0
			for _, b := range v.imgBytes {
				total += len(b)
			}
			return map[string]any{
				"model_id":          inner.GetModelID(),
				"image_count":       len(v.imgBytes),
				"image_bytes_total": total,
			}
		},
		LangfuseOutput: func(_ vlmInput, result string, _ error) any { return result },
		Usage: func(v vlmInput, result string) *langfuse.TokenUsage {
			// VLMs don't report token usage; approximate for cost dashboards.
			prompt := len([]rune(v.prompt))/4 + 1
			out := len([]rune(result)) / 4
			return &langfuse.TokenUsage{
				Input:  prompt,
				Output: out,
				Total:  prompt + out,
				Unit:   "TOKENS",
			}
		},
		DebugRecord: func(v vlmInput, response string, callErr error, dur time.Duration) *logger.LLMCallRecord {
			return buildVLMDebugRecord(inner.GetModelName(), v.imgBytes, v.prompt, response, callErr, dur)
		},
	}
	return observe.Wrap(ctx, call, in, func(ctx context.Context, _ vlmInput) (string, error) {
		return inner.Predict(ctx, imgBytes, prompt)
	})
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

func buildVLMDebugRecord(model string, imgBytes [][]byte, prompt, response string, callErr error, dur time.Duration) *logger.LLMCallRecord {
	record := &logger.LLMCallRecord{
		CallType: "VLM",
		Model:    model,
		Duration: dur,
	}

	var inputBuf strings.Builder
	fmt.Fprintf(&inputBuf, "Images: count=%d", len(imgBytes))
	total := 0
	for _, img := range imgBytes {
		total += len(img)
	}
	fmt.Fprintf(&inputBuf, ", total_size=%d bytes\n\n", total)
	inputBuf.WriteString("[prompt]\n")
	inputBuf.WriteString(prompt)
	inputBuf.WriteString("\n")
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Input", Content: inputBuf.String()})

	if response != "" {
		record.Sections = append(record.Sections, logger.RecordSection{
			Title:   "Response",
			Content: response,
		})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	return record
}
