package asr

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/internal/observe"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// ASR only has Langfuse instrumentation (no LLM debug logs), so the wrapper is
// small. Previously langfuse_wrapper.go; reshaped to go through observe.Wrap
// for consistency with the other sub-packages.

type asrInput struct {
	audioBytes []byte
	fileName   string
}

type langfuseASR struct {
	inner ASR
}

func (l *langfuseASR) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseASR) GetModelID() string   { return l.inner.GetModelID() }

func (l *langfuseASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error) {
	in := asrInput{audioBytes: audioBytes, fileName: fileName}
	call := observe.Call[asrInput, *TranscriptionResult]{
		Name:    "asr.transcribe",
		Model:   l.inner.GetModelName(),
		ModelID: l.inner.GetModelID(),
		LangfuseInput: func(v asrInput) any {
			return map[string]any{
				"file_name":  v.fileName,
				"audio_size": len(v.audioBytes),
			}
		},
		LangfuseMetadata: func(v asrInput) map[string]any {
			return map[string]any{
				"model_id":   l.inner.GetModelID(),
				"audio_size": len(v.audioBytes),
			}
		},
		LangfuseOutput: func(_ asrInput, result *TranscriptionResult, _ error) any {
			out := map[string]any{}
			if result != nil {
				out["text"] = result.Text
				out["segment_count"] = len(result.Segments)
				if n := len(result.Segments); n > 0 {
					out["duration_seconds"] = result.Segments[n-1].End
				}
			}
			return out
		},
		Usage: func(_ asrInput, result *TranscriptionResult) *langfuse.TokenUsage {
			// ASR is billed per second; report audio duration as Output usage
			// with unit=SECONDS so users can configure per-minute pricing.
			if result == nil || len(result.Segments) == 0 {
				return nil
			}
			duration := result.Segments[len(result.Segments)-1].End
			if duration <= 0 {
				return nil
			}
			seconds := int(duration + 0.5)
			return &langfuse.TokenUsage{
				Output: seconds,
				Total:  seconds,
				Unit:   "SECONDS",
			}
		},
	}
	return observe.Wrap(ctx, call, in, func(ctx context.Context, _ asrInput) (*TranscriptionResult, error) {
		return l.inner.Transcribe(ctx, audioBytes, fileName)
	})
}

// wrapASRLangfuse applies the Langfuse decorator when the manager is enabled.
func wrapASRLangfuse(a ASR, err error) (ASR, error) {
	if err != nil || a == nil {
		return a, err
	}
	if !langfuse.GetManager().Enabled() {
		return a, nil
	}
	return &langfuseASR{inner: a}, nil
}
