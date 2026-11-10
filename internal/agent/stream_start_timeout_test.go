package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func startupTestEngine(t *testing.T, handler http.HandlerFunc) *AgentEngine {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AgentEngine{
		config: &types.AgentConfig{LLMCallTimeout: 1},
		chatModel: openaicompletions.New(openaicompletions.Config{Endpoint: api.Endpoint{
			BaseURL: server.URL, Model: "timeout-test", Client: server.Client(),
		}}),
		modelContext: modelcontext.NewRegistry(false),
	}
}

func TestAgentStartupCancellationIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := startupTestEngine(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	})
	_, err := engine.streamLLMToEventBus(ctx, nil, &chat.ChatOptions{}, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, isTransientError(err))
	require.NotContains(t, err.Error(), "stalled")
}

func TestAgentStartupPreservesProviderError(t *testing.T) {
	engine := startupTestEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	})
	_, err := engine.streamLLMToEventBus(context.Background(), nil, &chat.ChatOptions{}, nil)
	var httpErr *api.HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
	require.False(t, isTransientError(err))
}

func TestAgentHeadersDoNotResetFirstOutputBudget(t *testing.T) {
	const headerDelay = 700 * time.Millisecond
	engine := startupTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(headerDelay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := engine.streamLLMToEventBus(ctx, nil, &chat.ChatOptions{}, nil)
	require.ErrorContains(t, err, "stalled")
	require.Less(t, time.Since(start), 1500*time.Millisecond)
	require.NoError(t, ctx.Err())
}

func TestAgentStartupAllowsLongActiveHTTPStream(t *testing.T) {
	const chunks = 8
	const gap = 200 * time.Millisecond
	engine := startupTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range chunks {
			select {
			case <-time.After(gap):
			case <-r.Context().Done():
				return
			}
			_, _ = fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"hello "}}]}`+"\n\n")
			w.(http.Flusher).Flush()
		}
		_, _ = fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	result, err := engine.streamLLMToEventBus(ctx, nil, &chat.ChatOptions{}, nil)
	require.NoError(t, err)
	require.Greater(t, time.Since(start), engine.getLLMStallTimeout())
	require.Equal(t, "stop", result.FinishReason)
	require.Len(t, result.Content, chunks*len("hello "))
}

func TestAgentStallIncludesHTTPHeaders(t *testing.T) {
	engine := startupTestEngine(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	messages := []chat.Message{{Role: "user", Content: "hello"}}
	_, err := engine.streamLLMToEventBus(ctx, messages, &chat.ChatOptions{}, nil)
	t.Logf("stall_timeout=1s; elapsed=%s; error=%v", time.Since(start), err)
	require.ErrorContains(t, err, "stalled", "the idle watchdog must cover waiting for HTTP response headers")
	require.NoError(t, ctx.Err(), "the per-call stall must not cancel the parent")
	require.True(t, isTransientError(err), "startup stalls must use the existing retry path")
}
