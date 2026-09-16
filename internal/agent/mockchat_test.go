package agent

// mockchat_test.go: ports main's mockChat test double onto the v2 invocation
// seam. main's engine accepted a chat.Chat implementation directly; the v2
// engine goes through invoke.ChatStream + a ModelConfig, so the mock runs its
// own fake-provider server (invoketest wire protocol: JSON ChatOptions in,
// NDJSON StreamEvents out), records the decoded calls, and replays the
// scripted types.StreamResponse chunks converted into invoke.StreamEvents.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/Tencent/WeKnora/internal/types"
)

// testLLMSeam is anything that can produce the ModelConfig handed to the
// engine: *invoketest.Fake, *mockChat, or a bare config wrapper.
type testLLMSeam interface {
	Config() *invoke.ModelConfig
}

// configSeam wraps a caller-built ModelConfig (e.g. a real vendor adapter
// pointed at an httptest server).
type configSeam struct{ cfg *invoke.ModelConfig }

func (s configSeam) Config() *invoke.ModelConfig { return s.cfg }

// mockResponse mirrors main's shape: one scripted LLM call, chunk by chunk.
type mockResponse struct {
	chunks []types.StreamResponse
}

type mockChat struct {
	mu        sync.Mutex
	responses []mockResponse
	calls     [][]invoke.Message
	opts      []*invoke.ChatOptions
	callCount int

	startOnce sync.Once
	fake      *invoketest.Fake
	srv       *httptest.Server
	cfg       *invoke.ModelConfig
}

// start boots the fake registry (provider seam + SSRF gate) and the local
// server. Called by newTestEngine; tests construct mockChat as a plain struct.
func (m *mockChat) start(t *testing.T) {
	t.Helper()
	m.startOnce.Do(func() {
		m.fake = invoketest.New(t) // registers the "fake" provider + SSRF loopback gate
		m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
		t.Cleanup(m.srv.Close)
		m.cfg = &invoke.ModelConfig{
			Provider:  "fake",
			ModelID:   "m-1",
			ModelName: "fake-model",
			BaseURL:   m.srv.URL,
		}
	})
}

// Config returns the ModelConfig to hand the engine (newTestEngine seam).
func (m *mockChat) Config() *invoke.ModelConfig { return m.cfg }

func (m *mockChat) handle(w http.ResponseWriter, r *http.Request) {
	var opts invoke.ChatOptions
	_ = json.NewDecoder(r.Body).Decode(&opts)

	m.mu.Lock()
	m.calls = append(m.calls, opts.Messages)
	optsCopy := opts
	m.opts = append(m.opts, &optsCopy)
	var resp mockResponse
	if m.callCount < len(m.responses) {
		resp = m.responses[m.callCount]
	} else {
		resp = mockResponse{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeError,
			Content: fmt.Sprintf("unexpected LLM call #%d (only %d responses prepared)",
				m.callCount, len(m.responses)),
			Done: true,
		}}}
	}
	m.callCount++
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher := w.(http.Flusher)
	for _, event := range mockChunksToStreamEvents(resp.chunks) {
		line, err := json.Marshal(event)
		if err != nil {
			continue
		}
		_, _ = w.Write(append(line, '\n'))
		flusher.Flush()
	}
}

// mockChunksToStreamEvents converts main-style StreamResponse chunks into the
// invoke.StreamEvents whose entry mapping reproduces the same chunk sequence:
//   - an answer chunk with Done:true and content becomes one StreamEvent whose
//     mapping emits the text fragment and then the Done frame (tool calls and
//     finish reason ride the Done, like main's single chunk);
//   - a tool-call chunk becomes one StreamKindToolCall event per call.
func mockChunksToStreamEvents(chunks []types.StreamResponse) []invoke.StreamEvent {
	events := make([]invoke.StreamEvent, 0, len(chunks))
	for _, chunk := range chunks {
		switch chunk.ResponseType {
		// An unset ResponseType is an answer: main's tests omit it for plain
		// text chunks and the engine treats the zero value as an answer.
		case types.ResponseTypeAnswer, types.ResponseType(""):
			ev := invoke.StreamEvent{Kind: invoke.StreamKindAnswer}
			if chunk.Content != "" {
				ev.Delta = &invoke.ContentDelta{Text: chunk.Content}
			}
			if chunk.Done {
				done := &invoke.FinishInfo{FinishReason: chunk.FinishReason}
				for _, call := range chunk.ToolCalls {
					done.ToolCalls = append(done.ToolCalls, invoke.ToolCall{
						ID:       call.ID,
						Type:     call.Type,
						Function: invoke.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments},
					})
				}
				ev.Done = done
			} else if ev.Delta == nil {
				continue
			}
			// A non-Done chunk may already carry complete tool calls (main's
			// tests stream them before any Done frame): emit them as
			// StreamKindToolCall so the entry assembles them like the real
			// engine path does.
			if !chunk.Done {
				for i, call := range chunk.ToolCalls {
					events = append(events, invoke.StreamEvent{
						Kind: invoke.StreamKindToolCall,
						ToolCallDelta: &invoke.ToolCallDelta{
							Index:     i,
							ID:        call.ID,
							Type:      call.Type,
							Name:      call.Function.Name,
							Arguments: call.Function.Arguments,
						},
					})
				}
			}
			events = append(events, ev)
		case types.ResponseTypeThinking:
			events = append(events, invoke.StreamEvent{
				Kind:  invoke.StreamKindThinking,
				Delta: &invoke.ContentDelta{Text: chunk.Content},
			})
		case types.ResponseTypeToolCall:
			for i, call := range chunk.ToolCalls {
				events = append(events, invoke.StreamEvent{
					Kind: invoke.StreamKindToolCall,
					ToolCallDelta: &invoke.ToolCallDelta{
						Index:     i,
						ID:        call.ID,
						Type:      call.Type,
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				})
			}
		case types.ResponseTypeError:
			ev := invoke.StreamEvent{
				Kind:  invoke.StreamKindError,
				Delta: &invoke.ContentDelta{Text: chunk.Content},
			}
			if chunk.Done {
				ev.Done = &invoke.FinishInfo{}
			}
			events = append(events, ev)
		}
	}
	return events
}

var _ = context.Background
