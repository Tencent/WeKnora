package invoke

// Stream-bridge tests: demuxer (SSE + NDJSON), the openai-shape default
// TranslateStreamEvent, tool-call delta assembly, and the entry's
// StreamEvent→StreamResponse mapping over a live httptest SSE stream.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strReader(s string) io.Reader { return strings.NewReader(s) }

func sseChunk(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return "data: " + string(b) + "\n\n"
}

func TestDemuxerSSE(t *testing.T) {
	body := "data: {\"a\":1}\n\nevent: error\ndata: boom\n\n" +
		": comment\ndata:no-space\n\ndata: [DONE]\n"
	d := NewDemuxer("text/event-stream", strReader(body))
	c1, ok := d.Next()
	require.True(t, ok)
	assert.Equal(t, `{"a":1}`, string(c1.Data))
	assert.Equal(t, "", c1.Event)
	c2, ok := d.Next()
	require.True(t, ok)
	assert.Equal(t, "error", c2.Event)
	assert.Equal(t, "boom", string(c2.Data))
	c3, ok := d.Next()
	require.True(t, ok)
	assert.Equal(t, "no-space", string(c3.Data))
	c4, ok := d.Next()
	require.True(t, ok)
	assert.Equal(t, "done", c4.Event)
	_, ok = d.Next()
	assert.False(t, ok)
	assert.NoError(t, d.Err())
}

func TestDemuxerNDJSON(t *testing.T) {
	body := "{\"line\":1}\n\n{\"line\":2}\n"
	d := NewDemuxer("application/json", strReader(body))
	c1, ok := d.Next()
	require.True(t, ok)
	assert.Equal(t, "", c1.Event)
	assert.Equal(t, `{"line":1}`, string(c1.Data))
	c2, ok := d.Next()
	require.True(t, ok)
	assert.Equal(t, `{"line":2}`, string(c2.Data))
	_, ok = d.Next()
	assert.False(t, ok)
}

// firstEvent adapts the multi-event bridge to the single-event fixtures
// below (single-kind frames yield exactly one event; mixed frames are
// covered explicitly by TestOpenAIBridgeMixedDelta).
func firstEvent[B interface {
	TranslateStreamEvent(state *StreamBridgeState, chunk StreamChunk) ([]*StreamEvent, error)
}](t *testing.T, b B, state *StreamBridgeState, chunk StreamChunk) *StreamEvent {
	t.Helper()
	evs, err := b.TranslateStreamEvent(state, chunk)
	require.NoError(t, err)
	if len(evs) == 0 {
		return nil
	}
	return evs[0]
}

func TestOpenAIBridgeTranslate(t *testing.T) {
	b := OpenAIStreamBridge{}
	state := NewStreamBridgeState()

	// Reasoning delta → thinking.
	ev := firstEvent(t, b, state, StreamChunk{Data: []byte(
		`{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`)})
	assert.Equal(t, StreamKindThinking, ev.Kind)
	assert.Equal(t, "hmm", ev.Delta.Text)

	// Answer delta.
	ev = firstEvent(t, b, state, StreamChunk{Data: []byte(
		`{"choices":[{"delta":{"content":"hi"}}]}`)})
	assert.Equal(t, StreamKindAnswer, ev.Kind)

	// Tool-call fragments merge across frames (fixtures built via
	// json.Marshal to keep the nesting honest).
	toolChunk := func(tc map[string]any) StreamChunk {
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"tool_calls": []any{tc}},
		}}})
		return StreamChunk{Data: data}
	}
	_, err := b.TranslateStreamEvent(state, toolChunk(map[string]any{
		"index": 0, "id": "c1", "type": "function",
		"function": map[string]any{"name": "get", "arguments": "{\"q"},
	}))
	require.NoError(t, err)
	_, err = b.TranslateStreamEvent(state, toolChunk(map[string]any{
		"index":    0,
		"function": map[string]any{"arguments": "\":1}"},
	}))
	require.NoError(t, err)

	// finish_reason frame stores state, emits nothing.
	ev = firstEvent(t, b, state, StreamChunk{Data: []byte(
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)})
	assert.Nil(t, ev)

	// Usage-only frame.
	ev = firstEvent(t, b, state, StreamChunk{Data: []byte(
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)})
	assert.Equal(t, StreamKindUsage, ev.Kind)
	assert.Equal(t, 7, ev.Usage.TotalTokens)

	// [DONE] closes with stored finish reason + assembled tool calls.
	ev = firstEvent(t, b, state, StreamChunk{Event: "done"})
	require.NotNil(t, ev.Done)
	assert.Equal(t, "tool_calls", ev.Done.FinishReason)
	require.Len(t, ev.Done.ToolCalls, 1)
	assert.Equal(t, "c1", ev.Done.ToolCalls[0].ID)
	assert.Equal(t, `{"q":1}`, ev.Done.ToolCalls[0].Function.Arguments)
}

func TestChatStreamEndToEnd(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(
			sseChunk(t, map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"reasoning_content": "think "},
			}}}) +
				sseChunk(t, map[string]any{"choices": []any{map[string]any{
					"delta": map[string]any{"content": "answer"},
				}}}) +
				sseChunk(t, map[string]any{"choices": []any{map[string]any{
					"delta": map[string]any{}, "finish_reason": "stop",
				}}}) +
				"data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()
	registerFake(t, &fakeChatAdapter{baseURL: srv.URL})

	ch, err := ChatStream(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	require.NoError(t, err)
	require.NotNil(t, ch)

	var answers, thinkings int
	var done *types.StreamResponse
	for resp := range ch {
		switch {
		case resp.ResponseType == types.ResponseTypeThinking:
			thinkings++
			assert.Equal(t, "think ", resp.Content)
		case resp.ResponseType == types.ResponseTypeAnswer && !resp.Done:
			answers++
			assert.Equal(t, "answer", resp.Content)
		case resp.Done:
			sr := resp
			done = &sr
		}
	}
	assert.Equal(t, 1, thinkings)
	assert.Equal(t, 1, answers)
	require.NotNil(t, done)
	assert.Equal(t, "stop", done.FinishReason)
}

func TestMapStreamEventKinds(t *testing.T) {
	// Error events surface as ResponseTypeError and close the stream.
	out := mapStreamEvent(&StreamEvent{Kind: StreamKindError, Delta: &ContentDelta{Text: "boom"}})
	require.Len(t, out, 1)
	assert.Equal(t, types.ResponseTypeError, out[0].ResponseType)
	assert.True(t, out[0].Done)

	// Incomplete finish maps to FinishReasonIncomplete.
	out = mapStreamEvent(&StreamEvent{Kind: StreamKindAnswer, Done: &FinishInfo{Incomplete: true}})
	require.Len(t, out, 1)
	assert.Equal(t, types.FinishReasonIncomplete, out[0].FinishReason)

	// Tool-call deltas surface as ResponseTypeToolCall.
	out = mapStreamEvent(&StreamEvent{
		Kind:          StreamKindToolCall,
		ToolCallDelta: &ToolCallDelta{Index: 0, ID: "t1", Type: "function", Name: "f", Arguments: "{}"},
	})
	require.Len(t, out, 1)
	require.Len(t, out[0].ToolCalls, 1)
	assert.Equal(t, "t1", out[0].ToolCalls[0].ID)
}

func TestChatStreamAbandonedByConsumer(t *testing.T) {
	allowLoopbackSSRF(t)
	// Consumer abandonment must not leak the producer goroutine or the
	// concurrency slot: cancelling ctx closes the stream reader (releasing
	// the slot) and the output channel drains to a close.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(sseChunk(t, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "a"}}},
		})))
		f.Flush()
		// Never end the stream voluntarily; unblock when the client
		// disconnects so srv.Close() is not stuck waiting on this handler.
		<-r.Context().Done()
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := ChatStream(ctx, testModelConfig(srv.URL), &ChatOptions{})
	require.NoError(t, err)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("no stream events")
	}
	cancel()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed: no leak
			}
		case <-time.After(2 * time.Second):
			t.Fatal("output channel did not close after consumer cancellation")
		}
	}
}

func TestChatStreamWithoutCallerDeadline(t *testing.T) {
	// Regression (executor stream-ctx bug): with no caller deadline, the
	// executor's fallback timeout cancel must stay alive until the stream is
	// consumed — a delayed second chunk arrives AFTER Do has returned, and a
	// cancel-at-return would kill the read with "stream interrupted".
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(sseChunk(t, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "one"}}},
		})))
		f.Flush()
		time.Sleep(150 * time.Millisecond) // second chunk lands after Do returns
		_, _ = w.Write([]byte(
			sseChunk(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "two"}}}}) +
				sseChunk(t, map[string]any{"choices": []any{map[string]any{
					"delta": map[string]any{}, "finish_reason": "stop",
				}}}) +
				"data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	// context.Background(): no deadline anywhere upstream.
	ch, err := ChatStream(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	require.NoError(t, err)
	var contents []string
	var finish string
	for resp := range ch {
		if resp.ResponseType == types.ResponseTypeAnswer && !resp.Done {
			contents = append(contents, resp.Content)
		}
		if resp.Done {
			finish = resp.FinishReason
		}
	}
	assert.Equal(t, []string{"one", "two"}, contents, "both chunks must survive the post-Do read")
	assert.Equal(t, "stop", finish)
}

func TestSlotReleasingReaderReleasesOnce(t *testing.T) {
	// EOF or Close — whichever first — releases exactly once.
	released := 0
	r := newSlotReleasingReader(&nopReadCloser{data: "ab"})
	r.attachRelease(func() { released++ })
	_, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, 1, released)
	require.NoError(t, r.Close()) // second release attempt must be a no-op
	assert.Equal(t, 1, released)
}

type nopReadCloser struct {
	data   string
	offset int
}

func (n *nopReadCloser) Read(p []byte) (int, error) {
	if n.offset >= len(n.data) {
		return 0, io.EOF
	}
	c := copy(p, n.data[n.offset:])
	n.offset += c
	return c, nil
}

func (n *nopReadCloser) Close() error { return nil }

// TestOpenAIBridgeMixedDelta pins the multi-event bridge (2026-09-13 裁定):
// a mixed delta emits EVERY payload it carries — tool calls (one event per
// delta), then reasoning, then content — instead of silently dropping the
// tails (the one-event-per-frame era lost content/tool_calls behind
// reasoning_content).
func TestOpenAIBridgeMixedDelta(t *testing.T) {
	b := OpenAIStreamBridge{}
	state := NewStreamBridgeState()
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{
			"reasoning_content": "hmm",
			"content":           "hi",
			"tool_calls": []any{
				map[string]any{
					"index": 0, "id": "c1", "type": "function",
					"function": map[string]any{"name": "get", "arguments": "{}"},
				},
				map[string]any{
					"index": 1, "id": "c2", "type": "function",
					"function": map[string]any{"name": "put", "arguments": "{}"},
				},
			},
		},
	}}})
	evs, err := b.TranslateStreamEvent(state, StreamChunk{Data: data})
	require.NoError(t, err)
	require.Len(t, evs, 4)
	assert.Equal(t, StreamKindToolCall, evs[0].Kind)
	assert.Equal(t, "c1", evs[0].ToolCallDelta.ID)
	assert.Equal(t, StreamKindToolCall, evs[1].Kind)
	assert.Equal(t, "c2", evs[1].ToolCallDelta.ID)
	assert.Equal(t, StreamKindThinking, evs[2].Kind)
	assert.Equal(t, "hmm", evs[2].Delta.Text)
	assert.Equal(t, StreamKindAnswer, evs[3].Kind)
	assert.Equal(t, "hi", evs[3].Delta.Text)
}
