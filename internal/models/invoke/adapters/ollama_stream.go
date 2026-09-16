package adapters

// ollama_stream.go: the Ollama NDJSON stream bridge, ported from v1
// OllamaChat.ChatStream. Ollama returns complete tool calls (not deltas) and
// drives thinking via message.thinking (Qwen3/DeepSeek reasoning models).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TranslateStreamEvent bridges one NDJSON line. Event layout per v1:
// message.thinking → thinking deltas; message.content → answer deltas;
// tool_calls → complete tool-call events; the done line → Done with usage.
//
// Multi-event bridge (2026-09-13 裁定)：一行携带多个 payload 时全部发出
// （tool_calls 每调用一个事件——多调用/行不再截断，thinking 与 content
// 混行也依序发出）；Done 恒为末元素。
func (a *OllamaAdapter) TranslateStreamEvent(
	_ *invoke.StreamBridgeState, chunk invoke.StreamChunk,
) ([]*invoke.StreamEvent, error) {
	var ev ollamaChatResponse
	if err := decodeChunk(chunk.Data, &ev); err != nil {
		// v1 aborted the stream on decode failures; the entry stops once
		// Done is set.
		return []*invoke.StreamEvent{{
			Kind:  invoke.StreamKindError,
			Delta: &invoke.ContentDelta{Text: err.Error()},
			Done:  &invoke.FinishInfo{},
		}}, nil
	}
	var out []*invoke.StreamEvent
	if ev.Message.Thinking != "" {
		out = append(out, &invoke.StreamEvent{
			Kind:  invoke.StreamKindThinking,
			Delta: &invoke.ContentDelta{Text: ev.Message.Thinking},
		})
	}
	if len(ev.Message.ToolCalls) > 0 {
		for _, c := range ev.Message.ToolCalls {
			args, _ := jsonMarshal(c.Function.Arguments)
			out = append(out, &invoke.StreamEvent{
				Kind: invoke.StreamKindToolCall,
				ToolCallDelta: &invoke.ToolCallDelta{
					ID:        tooli2s(c.Function.Index),
					Type:      "function",
					Name:      c.Function.Name,
					Arguments: args,
				},
			})
		}
	}
	if ev.Message.Content != "" {
		out = append(out, &invoke.StreamEvent{
			Kind:  invoke.StreamKindAnswer,
			Delta: &invoke.ContentDelta{Text: ev.Message.Content},
		})
	}
	if len(out) > 0 {
		return out, nil
	}
	if ev.Done {
		return []*invoke.StreamEvent{doneEvent(&ev)}, nil
	}
	return nil, nil
}

// doneEvent ports v1 stream usage arithmetic (prompt=PromptEvalCount,
// completion=EvalCount — NOT the non-stream subtraction). Usage rides the
// Done event (v1 emitted a single Done chunk with usage).
func doneEvent(ev *ollamaChatResponse) *invoke.StreamEvent {
	event := &invoke.StreamEvent{
		Kind: invoke.StreamKindAnswer,
		Done: &invoke.FinishInfo{},
	}
	if ev.PromptEvalCount > 0 || ev.EvalCount > 0 {
		event.Usage = &invoke.Usage{
			PromptTokens:     ev.PromptEvalCount,
			CompletionTokens: ev.EvalCount,
			TotalTokens:      ev.PromptEvalCount + ev.EvalCount,
		}
	}
	return event
}

// resolveImageForOllama ports v1 image resolution: data URIs decode inline,
// application-stored schemes resolve through the entry's LocalImageResolver,
// remote URLs download through the SSRF-safe client. Failures return nil and
// the image is dropped (v1 semantics).
func resolveImageForOllama(imageURL string) []byte {
	if data := resolveStoredImageForOllama(imageURL); data != nil {
		return data
	}
	if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		if err := secutils.ValidateURLForSSRF(imageURL); err != nil {
			return nil
		}
		client := secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
			Timeout:      30 * time.Second,
			MaxRedirects: 5,
		})
		resp, err := client.Get(imageURL)
		if err != nil {
			return nil
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
		if err != nil {
			return nil
		}
		return data
	}
	return nil
}

func resolveStoredImageForOllama(imageURL string) []byte {
	if strings.HasPrefix(imageURL, "data:") {
		idx := strings.Index(imageURL, ";base64,")
		if idx < 0 {
			return nil
		}
		decoded, err := base64.StdEncoding.DecodeString(imageURL[idx+8:])
		if err != nil {
			return nil
		}
		return decoded
	}
	if invoke.IsApplicationStoredImage(imageURL) && invoke.LocalImageResolver != nil {
		if data, ok := invoke.LocalImageResolver(imageURL); ok {
			return data
		}
	}
	return nil
}

// jsonMarshal keeps the tool-call argument marshaling failure explicit.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal tool call arguments: %w", err)
	}
	return string(b), nil
}
