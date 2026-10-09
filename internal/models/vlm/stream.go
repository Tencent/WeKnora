package vlm

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// StreamChunk is one piece of a streaming VLM response surfaced to the manager.
// The manager consumes streaming whenever the underlying VLM exposes it — even
// when the caller only asked for a buffered result — so it can observe the
// first token / TTFT and server health without changing the caller contract.
type StreamChunk struct {
	Text  string
	Done  bool
	Err   error
	Usage *types.TokenUsage
}

// StreamingVLM is implemented by VLMs that can stream tokens to the manager.
// It is additive: the base VLM interface (Predict / PredictWithOptions) stays
// the contract for callers; this only lets the manager observe streaming.
type StreamingVLM interface {
	PredictStream(
		ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions,
	) (<-chan StreamChunk, error)
}

// PredictStream streams a vision request through the chat client. It is used by
// the manager for first-token / TTFT observation and (when the caller wants an
// incremental UX) for token forwarding.
//
// It mirrors the buffered PredictWithOptions markers: a "[VLM] Calling chat
// protocol" line is emitted before the request is sent, and a terminal line is
// emitted when the stream ends — a "[VLM] response received" on success, or a
// "[VLM] stream ended" carrying the error otherwise. The two lines bracket a
// single call so its round-trip can be timed from logs; without them, streaming
// VLM calls (flash, qwen) left no per-call duration evidence at all.
func (v *RemoteAPIVLM) PredictStream(
	ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions,
) (<-chan StreamChunk, error) {
	parts := []chat.MessageContentPart{{Type: "text", Text: prompt}}
	totalImageSize := 0
	for _, b := range imgBytes {
		if len(b) == 0 {
			continue
		}
		totalImageSize += len(b)
		dataURI := fmt.Sprintf("data:%s;base64,%s",
			detectImageMIME(b), base64.StdEncoding.EncodeToString(b))
		parts = append(parts, chat.MessageContentPart{
			Type:     "image_url",
			ImageURL: &chat.ImageURL{URL: dataURI, Detail: "auto"},
		})
	}

	chatOpts := &chat.ChatOptions{
		Temperature: v.temperature,
		MaxTokens:   defaultMaxToks,
	}
	if opts != nil && opts.Thinking != nil {
		chatOpts.Thinking = opts.Thinking
	}

	ctx, cancel := context.WithTimeout(ctx, vlmHTTPTimeout())
	// Same request marker the buffered path emits, so streaming and buffered VLM
	// calls are observable identically.
	logger.Infof(ctx, "[VLM] Calling chat protocol, model=%s, numImages=%d, totalImageSize=%d",
		v.modelName, len(imgBytes), totalImageSize)

	ch, err := v.chat.ChatStream(ctx, []chat.Message{{Role: "user", MultiContent: parts}}, chatOpts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("VLM stream: %w", err)
	}

	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		defer cancel()
		var totalLen int
		for resp := range ch {
			if resp.ResponseType == types.ResponseTypeError {
				out <- StreamChunk{Err: fmt.Errorf("VLM stream error: %s", resp.Content)}
				logger.Infof(ctx, "[VLM] stream ended, len=%d, err=%s", totalLen, resp.Content)
				return
			}
			totalLen += len(resp.Content)
			out <- StreamChunk{
				Text:  resp.Content,
				Done:  resp.Done,
				Usage: resp.Usage,
				Err:   nil,
			}
			if resp.Done {
				logger.Infof(ctx, "[VLM] response received, len=%d", totalLen)
				return
			}
			if resp.FinishReason == types.FinishReasonIncomplete {
				// Provider cut the stream without a finish reason: the
				// accumulated text is partial; report it as a transport error
				// so the caller retries rather than recording a half answer.
				out <- StreamChunk{Err: fmt.Errorf("%s", types.StreamEndedEarlyError)}
				logger.Infof(ctx, "[VLM] stream ended, len=%d, err=%s", totalLen, types.StreamEndedEarlyError)
				return
			}
		}
		// Channel closed with no terminal chunk: treat as incomplete.
		out <- StreamChunk{Err: fmt.Errorf("%s", types.StreamEndedEarlyError)}
		logger.Infof(ctx, "[VLM] stream ended, len=%d, err=%s", totalLen, types.StreamEndedEarlyError)
	}()
	return out, nil
}

// supportsStreaming reports whether the immediate inner implements StreamingVLM.
// The base RemoteAPIVLM does; the debug / langfuse decorators forward it, so
// this stays true through the usual wrapper chain.
func supportsStreaming(inner VLM) bool {
	_, ok := inner.(StreamingVLM)
	return ok
}

// --- debugVLM forwards streaming when its inner supports it ---

func (d *debugVLM) PredictStream(
	ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions,
) (<-chan StreamChunk, error) {
	if s, ok := d.inner.(StreamingVLM); ok {
		return s.PredictStream(ctx, imgBytes, prompt, opts)
	}
	return nil, fmt.Errorf("vlm: debugVLM inner does not support streaming")
}

// --- langfuseVLM forwards streaming when its inner supports it ---

func (l *langfuseVLM) PredictStream(
	ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions,
) (<-chan StreamChunk, error) {
	if s, ok := l.inner.(StreamingVLM); ok {
		return s.PredictStream(ctx, imgBytes, prompt, opts)
	}
	return nil, fmt.Errorf("vlm: langfuseVLM inner does not support streaming")
}
