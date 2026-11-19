package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const overflowQueryMarker = "CURRENT_TASK_EXPORT_ONLY_APPROVED_ROWS"

type overflowWireRecorder struct {
	mu       sync.Mutex
	requests []string
}

func (r *overflowWireRecorder) serve(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Messages []struct{ Content string }
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var parts []string
	for _, msg := range body.Messages {
		parts = append(parts, msg.Content)
	}
	text := strings.Join(parts, "\n")
	r.mu.Lock()
	r.requests = append(r.requests, text)
	first := len(r.requests) == 1
	r.mu.Unlock()
	if first {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"maximum context length exceeded",`+
			`"type":"invalid_request_error","code":"context_length_exceeded"}}`)
		return
	}
	answer := "Short summary of the available conversation."
	if strings.Contains(text, overflowQueryMarker) {
		answer += " " + overflowQueryMarker
	}
	encoded, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s},"+
		"\"finish_reason\":\"stop\"}]}\n\n", encoded)
}

func overflowMessages(withSummary bool) []chat.Message {
	messages := []chat.Message{{Role: "system", Content: "Follow the user's request."}}
	if withSummary {
		messages = append(messages, compaction.SummaryMessage("Earlier completed work."))
	}
	messages = append(messages, chat.Message{Role: "user", Content: overflowQueryMarker})
	for i := range 8 {
		id := fmt.Sprintf("overflow-call-%d", i)
		messages = append(messages,
			chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{{
				ID: id, Type: "function",
				Function: chat.FunctionCall{Name: "read_file", Arguments: `{"path":"/workspace/input.csv"}`},
			}}},
			chat.Message{
				Role: "tool", ToolCallID: id, Name: "read_file",
				Content: strings.Repeat("ordinary table row ", 450),
			},
		)
	}
	return messages
}

func TestOverflowRecoveryPreservesCurrentTask(t *testing.T) {
	for _, withSummary := range []bool{false, true} {
		t.Run(fmt.Sprintf("loaded_checkpoint_%v", withSummary), func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			recorder := &overflowWireRecorder{}
			server := httptest.NewServer(http.HandlerFunc(recorder.serve))
			defer server.Close()
			model := openaicompletions.New(openaicompletions.Config{Endpoint: api.Endpoint{
				BaseURL: server.URL, Model: "overflow-test", Client: server.Client(),
			}})
			engine := newTestEngine(t, model, withMaxContextTokens(40000), withMaxCompletionTokens(4000))
			engine.compactor = compaction.New(model, engine.tokenEstimator, compaction.Settings{
				Enabled: true, MaxContextTokens: 40000, ReserveTokens: 8000, KeepRecentTokens: 2000,
			})
			messages := overflowMessages(withSummary)
			before := engine.tokenEstimator.EstimateMessages(messages)
			response, err := engine.callLLMWithRetry(t.Context(), &messages, nil,
				&types.AgentState{}, overflowQueryMarker, 0, "overflow-session")
			require.NoError(t, err)
			require.NotNil(t, response)
			recorder.mu.Lock()
			requests := append([]string(nil), recorder.requests...)
			recorder.mu.Unlock()
			require.GreaterOrEqual(t, len(requests), 3)
			require.True(t, strings.Contains(requests[0], overflowQueryMarker))
			seenBySummarizer := false
			for _, request := range requests[1 : len(requests)-1] {
				seenBySummarizer = seenBySummarizer || strings.Contains(request, overflowQueryMarker)
			}
			seenOnRetry := strings.Contains(requests[len(requests)-1], overflowQueryMarker)
			t.Logf("HTTP requests=%d; before=%d after=%d; current task in summary input=%v; in retry=%v",
				len(requests), before, engine.tokenEstimator.EstimateMessages(messages), seenBySummarizer, seenOnRetry)
			assert.True(t, seenBySummarizer, "overflow recovery must give the current request to the summarizer")
			assert.True(t, seenOnRetry, "successful retry must retain the current task")
		})
	}
}

func TestOverflowRecoveryKeepsStructuredHistoryWithoutCompaction(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	recorder := &overflowWireRecorder{}
	server := httptest.NewServer(http.HandlerFunc(recorder.serve))
	defer server.Close()
	model := openaicompletions.New(openaicompletions.Config{Endpoint: api.Endpoint{
		BaseURL: server.URL, Model: "overflow-test", Client: server.Client(),
	}})
	engine := newTestEngine(t, model, withMaxContextTokens(40000))
	messages := overflowMessages(true)[:3]
	expected := append([]chat.Message(nil), messages...)
	response, err := engine.callLLMWithRetry(t.Context(), &messages, nil,
		&types.AgentState{}, overflowQueryMarker, 0, "overflow-session")
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, expected, messages, "a no-op compaction must retain summary/user boundaries")
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Len(t, recorder.requests, 2)
	assert.Contains(t, recorder.requests[1], overflowQueryMarker)
}
