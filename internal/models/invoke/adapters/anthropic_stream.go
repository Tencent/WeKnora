package adapters

// anthropic_stream.go: the Anthropic SSE bridge (TranslateStreamEvent) and
// the non-stream SSE aggregation, ported from v1 processAnthropicStream /
// parseAnthropicSSE. One internal StreamEvent per demuxed chunk; the final
// Done event carries the merged Usage (v1 emitted one Done chunk with usage —
// the entry-level mapping either carries it through or the reconciliation
// test normalizes the usage-then-done split as a documented delta).

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// decodeChunk is the local chunk decoder (invoke's helper is unexported).
func decodeChunk(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode stream chunk: %w", err)
	}
	return nil
}

// anthropicStreamAccum replicates v1 mergeAnthropicUsage arithmetic: the
// prompt counter holds uncached input plus cache read/write counters, each
// event merging by max.
type anthropicStreamAccum struct {
	prompt     int
	completion int
	read       int
	write      int
	// reported is true once a usage frame carried either cache counter —
	// the honest "vendor reported cache numbers" flag (absence of frames
	// must not fabricate zero-counters downstream).
	reported bool
}

func (a *anthropicStreamAccum) merge(u *anthropicUsageFields) {
	if u == nil {
		return
	}
	if u.CacheReadInputTokens != nil {
		a.read = max(a.read, *u.CacheReadInputTokens)
	}
	if u.CacheCreationInputTokens != nil {
		a.write = max(a.write, *u.CacheCreationInputTokens)
	}
	a.reported = a.reported || u.CacheReadInputTokens != nil || u.CacheCreationInputTokens != nil
	uncached := max(0, a.prompt-a.read-a.write)
	uncached = max(uncached, u.InputTokens)
	a.prompt = uncached + a.read + a.write
	a.completion = max(a.completion, u.OutputTokens)
}

func (a *anthropicStreamAccum) usage() *invoke.Usage {
	return &invoke.Usage{
		PromptTokens:     a.prompt,
		CompletionTokens: a.completion,
		TotalTokens:      a.prompt + a.completion,
		// v1 SetPromptCacheUsage parity (2026-09-13 review: the v2 port
		// dropped the four detail fields, leaving Anthropic's engine-level
		// cache_hit_rate stats permanently empty).
		CacheReadTokens:  a.read,
		CacheWriteTokens: a.write,
		CacheMissTokens:  max(0, a.prompt-a.read-a.write),
		CacheReported:    a.reported,
	}
}

func anthropicState(state *invoke.StreamBridgeState) *anthropicStreamAccum {
	if v, ok := state.Get("anthropicUsage"); ok {
		return v.(*anthropicStreamAccum)
	}
	acc := &anthropicStreamAccum{}
	state.Set("anthropicUsage", acc)
	return acc
}

func anthropicFinishReason(state *invoke.StreamBridgeState) string {
	if v, ok := state.Get("anthropicFinish"); ok {
		return v.(string)
	}
	return ""
}

func anthropicText(state *invoke.StreamBridgeState) string {
	if v, ok := state.Get("anthropicText"); ok {
		return v.(string)
	}
	return ""
}

// TranslateStreamEvent bridges one demuxed SSE frame. The executor's Demuxer
// yields Event="<anthropic event name>" frames; dispatch follows the decoded
// JSON "type" like v1 did. The [DONE] sentinel (Event="done") and
// message_stop both finalize the stream.
func (a *AnthropicAdapter) TranslateStreamEvent(
	state *invoke.StreamBridgeState, chunk invoke.StreamChunk,
) ([]*invoke.StreamEvent, error) {
	if chunk.Event == "done" {
		return []*invoke.StreamEvent{a.finalEvent(state)}, nil
	}
	var ev anthropicStreamEvent
	if err := decodeChunk(chunk.Data, &ev); err != nil {
		return nil, err
	}
	if ev.Error != nil && ev.Error.Message != "" {
		// v1 aborted the stream on in-band error chunks; the entry stops the
		// loop once Done is set, so mark the error event as terminal.
		return []*invoke.StreamEvent{{
			Kind:  invoke.StreamKindError,
			Delta: &invoke.ContentDelta{Text: ev.Error.Message},
			Done:  &invoke.FinishInfo{},
		}}, nil
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			anthropicState(state).merge(&ev.Message.Usage)
		}
	case "content_block_delta":
		if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
			return []*invoke.StreamEvent{{
				Kind:  invoke.StreamKindAnswer,
				Delta: &invoke.ContentDelta{Text: ev.Delta.Text},
			}}, nil
		}
	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			state.Set("anthropicFinish", ev.Delta.StopReason)
			state.Set(invoke.StreamStateFinishReason, ev.Delta.StopReason)
		}
		if ev.Usage != nil {
			anthropicState(state).merge(ev.Usage)
		}
		// v1 emitted usage only inside the final Done chunk. message_stop must
		// close the stream, so usage flows as its own event here (totals
		// identical: this is the last usage-carrying frame before
		// message_stop). The Done event also carries Usage.
		return []*invoke.StreamEvent{{
			Kind:  invoke.StreamKindUsage,
			Usage: anthropicState(state).usage(),
		}}, nil
	case "message_stop":
		return []*invoke.StreamEvent{a.finalEvent(state)}, nil
	}
	return nil, nil
}

// finalEvent closes the stream: answer Done with the merged usage and the
// captured stop_reason (v1 Done-chunk shape).
func (a *AnthropicAdapter) finalEvent(state *invoke.StreamBridgeState) *invoke.StreamEvent {
	return &invoke.StreamEvent{
		Kind:  invoke.StreamKindAnswer,
		Usage: anthropicState(state).usage(),
		Done:  &invoke.FinishInfo{FinishReason: anthropicFinishReason(state)},
	}
}

// aggregateAnthropicSSE ports v1 parseAnthropicSSE: fold an SSE body (arrived
// on a non-stream call) into one ChatResponse.
func aggregateAnthropicSSE(body []byte) (*invoke.ChatResponse, error) {
	demux := invoke.NewDemuxer("text/event-stream", bytes.NewReader(body))
	state := invoke.NewStreamBridgeState()
	for {
		chunk, ok := demux.Next()
		if !ok {
			break
		}
		if chunk.Event == "done" {
			break
		}
		var ev anthropicStreamEvent
		if err := decodeChunk(chunk.Data, &ev); err != nil {
			return nil, fmt.Errorf("decode SSE response: %w", err)
		}
		if ev.Error != nil && ev.Error.Message != "" {
			return nil, fmt.Errorf("API stream error: %s", ev.Error.Message)
		}
		if ev.Message != nil {
			anthropicState(state).merge(&ev.Message.Usage)
		}
		if ev.Delta != nil {
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				state.Set("anthropicText", anthropicText(state)+ev.Delta.Text)
			}
			if ev.Delta.StopReason != "" {
				state.Set("anthropicFinish", ev.Delta.StopReason)
				state.Set(invoke.StreamStateFinishReason, ev.Delta.StopReason)
			}
		}
		if ev.Usage != nil {
			anthropicState(state).merge(ev.Usage)
		}
	}
	acc := anthropicState(state)
	return &invoke.ChatResponse{
		Content:      anthropicText(state),
		FinishReason: anthropicFinishReason(state),
		Usage:        *acc.usage(),
	}, nil
}
