package compaction

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactionHTTPStreamCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int32
		wantCalls int32
		degraded  bool
	}{
		{name: "complete", wantCalls: 2},
		{name: "retry after EOF", failures: 1, wantCalls: 3},
		{name: "persistent EOF", failures: 4, wantCalls: 4, degraded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeSummarySSE(w, calls.Add(1) <= tc.failures)
			}))
			defer server.Close()
			model := openaicompletions.New(openaicompletions.Config{Endpoint: api.Endpoint{
				BaseURL: server.URL, Model: "summary-test", Client: server.Client(),
			}})
			c := New(model, newEstimator(t), testSettings())
			messages := withStoredHistory(reactTurn(12), storedTurn("stored-turn", 1, "/workspace/a.txt"))
			result, err := c.Compact(context.Background(), messages, ReasonThreshold)
			require.NoError(t, err)
			require.NotNil(t, result.Checkpoint)
			assert.Equal(t, tc.wantCalls, calls.Load())
			assert.Equal(t, tc.degraded, result.Degraded)
			assert.Equal(t, tc.degraded, result.Checkpoint.Degraded)
			assert.NotContains(t, result.Summary, "INCOMPLETE_SUMMARY")
			assert.NotContains(t, result.Checkpoint.Summary, "INCOMPLETE_SUMMARY")
			if tc.degraded {
				assert.Contains(t, result.Checkpoint.Summary, "Raw conversation archive")
				assert.Contains(t, result.Checkpoint.Summary, "question stored-turn")
			} else {
				assert.Contains(t, result.Checkpoint.Summary, "COMPLETE_SUMMARY")
			}
		})
	}
}

// The real provider must classify a clean HTTP EOF
// without finish_reason as incomplete before the compactor decides to commit.
func writeSummarySSE(w http.ResponseWriter, incomplete bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	if incomplete {
		_, _ = fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"INCOMPLETE_SUMMARY"}}]}`+"\n\n")
	} else {
		_, _ = fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"COMPLETE_SUMMARY"},`+
			`"finish_reason":"stop"}]}`+"\n\n")
	}
}
