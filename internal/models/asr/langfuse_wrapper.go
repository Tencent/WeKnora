package asr

import (
	"context"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// langfuseASR wraps an ASR implementation and reports each Transcribe call
// as a Langfuse generation observation. Audio bytes are not uploaded — we
// record file name, audio size and duration (derived from segment end times)
// so usage can be billed by second/minute, which is how most ASR providers
// price their services.
type langfuseASR struct {
	inner ASR
}

func (l *langfuseASR) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseASR) GetModelID() string   { return l.inner.GetModelID() }

func (l *langfuseASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.Transcribe(ctx, audioBytes, fileName)
	}

	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "asr.transcribe",
		Model: l.inner.GetModelName(),
		Input: map[string]interface{}{
			"file_name":  fileName,
			"audio_size": len(audioBytes),
		},
		Metadata: map[string]interface{}{
			"model_id":   l.inner.GetModelID(),
			"audio_size": len(audioBytes),
		},
	})

	result, err := l.inner.Transcribe(genCtx, audioBytes, fileName)

	output := map[string]interface{}{}
	var duration float64
	if result != nil {
		output["text"] = result.Text
		output["segment_count"] = len(result.Segments)
		if n := len(result.Segments); n > 0 {
			duration = result.Segments[n-1].End
			output["duration_seconds"] = duration
		}
	}

	// ASR is billed per second; we emit the audio duration (when available)
	// in the "Output" side of usage so users can configure per-model pricing
	// in Langfuse (e.g. $0.006 per minute for Whisper-1).
	var usage *langfuse.TokenUsage
	if duration > 0 {
		seconds := int(duration + 0.5)
		usage = &langfuse.TokenUsage{
			Output: seconds,
			Total:  seconds,
			Unit:   "SECONDS",
		}
	}

	gen.Finish(output, usage, err)
	return result, err
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
