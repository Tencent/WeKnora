package compaction

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func startupTestCompactor(t *testing.T, handler http.HandlerFunc) *Compactor {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	model := openaicompletions.New(openaicompletions.Config{Endpoint: api.Endpoint{
		BaseURL: server.URL, Model: "timeout-test", Client: server.Client(),
	}})
	s := testSettings()
	s.StallTimeout = 200 * time.Millisecond
	return New(model, newEstimator(t), s)
}

func TestCompactionStartupRetriesAndDegrades(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry recovers", true: "retry degrades"}[persistent], func(t *testing.T) {
			var calls atomic.Int32
			c := startupTestCompactor(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) == 1 || persistent {
					<-r.Context().Done()
					return
				}
				writeSummarySSE(w, false)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := c.Compact(ctx, reactTurn(12), ReasonThreshold)
			require.NoError(t, err)
			require.NoError(t, ctx.Err())
			require.EqualValues(t, maxSummarizationAttempts, calls.Load())
			require.Equal(t, persistent, result.Degraded)
			if persistent {
				require.Contains(t, result.Summary, "Raw conversation archive")
			} else {
				require.Contains(t, result.Summary, "COMPLETE_SUMMARY")
			}
		})
	}
}

func TestCompactionStartupCancellationIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	c := startupTestCompactor(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		cancel()
		<-r.Context().Done()
	})
	_, err := c.summarize(ctx, reactTurn(2), 0, "", initialSummarizationInstructions, 1024)
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, 1, calls.Load())
	require.NotContains(t, err.Error(), "stalled")
}

func TestCompactionStartupPreservesProviderError(t *testing.T) {
	c := startupTestCompactor(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	})
	_, _, err := c.streamSummary(context.Background(), nil, &chat.ChatOptions{})
	var httpErr *api.HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusUnauthorized, httpErr.StatusCode)
}

func TestCompactionHeadersDoNotResetFirstOutputBudget(t *testing.T) {
	const headerDelay = 700 * time.Millisecond
	c := startupTestCompactor(t, func(w http.ResponseWriter, r *http.Request) {
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
	c.settings.StallTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := c.streamSummary(ctx, nil, &chat.ChatOptions{})
	require.ErrorContains(t, err, "stalled")
	require.Less(t, time.Since(start), 1500*time.Millisecond)
	require.NoError(t, ctx.Err())
}

func TestCompactionStallIncludesHTTPHeaders(t *testing.T) {
	c := startupTestCompactor(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := c.streamSummary(ctx, []chat.Message{{Role: "user", Content: "summarize"}}, &chat.ChatOptions{})
	t.Logf("stall_timeout=%s; elapsed=%s; error=%v", c.settings.StallTimeout, time.Since(start), err)
	require.ErrorContains(t, err, "stalled", "the configured idle timeout must cover response headers")
	require.NoError(t, ctx.Err(), "the parent must remain usable for retry")
}
