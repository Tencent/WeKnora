package openaicompletions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamCalls runs the given SSE data lines through ChatStream and returns the
// tool calls of the final chunk.
func streamCalls(t *testing.T, lines ...string) []types.LLMToolCall {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			_, _ = w.Write([]byte("data: " + l + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL, Model: "m"}, Settings: api.DefaultOpenAICompletions(),
	})
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)

	var last types.StreamResponse
	for chunk := range ch {
		require.NotEqual(t, types.ResponseTypeError, chunk.ResponseType, chunk.Content)
		if chunk.Done {
			last = chunk
		}
	}
	return last.ToolCalls
}

func toolChunk(calls string, finish string) string {
	f := "null"
	if finish != "" {
		f = `"` + finish + `"`
	}
	return `{"choices":[{"delta":{"tool_calls":[` + calls + `]},"finish_reason":` + f + `}]}`
}

func TestChatStream_IndexedToolCallsStayApart(t *testing.T) {
	calls := streamCalls(t,
		toolChunk(`{"index":0,"id":"a","type":"function","function":{"name":"get_weather","arguments":""}}`, ""),
		toolChunk(`{"index":0,"function":{"arguments":"{\"city\":\"Paris\"}"}}`, ""),
		toolChunk(
			`{"index":1,"id":"b","type":"function","function":{"name":"get_time","arguments":"{}"}}`,
			"tool_calls"),
	)
	require.Len(t, calls, 2)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.Equal(t, `{"city":"Paris"}`, calls[0].Function.Arguments)
	assert.Equal(t, "get_time", calls[1].Function.Name)
}

func TestChatStream_ParallelCallsWithoutIndexStayApart(t *testing.T) {
	// Each call arrives whole in its own chunk and no chunk has an index.
	calls := streamCalls(t,
		toolChunk(
			`{"id":"a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}`,
			""),
		toolChunk(
			`{"id":"b","type":"function","function":{"name":"get_time","arguments":"{\"tz\":\"UTC\"}"}}`,
			"tool_calls"),
	)
	require.Len(t, calls, 2)
	assert.Equal(t, "a", calls[0].ID)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.Equal(t, `{"city":"Paris"}`, calls[0].Function.Arguments)
	assert.Equal(t, "b", calls[1].ID)
	assert.Equal(t, "get_time", calls[1].Function.Name)
	assert.Equal(t, `{"tz":"UTC"}`, calls[1].Function.Arguments)
}

func TestChatStream_TwoCallsInOneChunkWithoutIndexStayApart(t *testing.T) {
	calls := streamCalls(t,
		toolChunk(
			`{"id":"a","type":"function","function":{"name":"get_weather","arguments":"{}"}},`+
				`{"id":"b","type":"function","function":{"name":"get_time","arguments":"{}"}}`,
			"tool_calls"),
	)
	require.Len(t, calls, 2)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.Equal(t, "get_time", calls[1].Function.Name)
}

func TestChatStream_FragmentsWithoutIndexContinueTheCall(t *testing.T) {
	// Name and id first, then argument fragments with neither, or with the
	// id repeated.
	calls := streamCalls(t,
		toolChunk(`{"id":"a","type":"function","function":{"name":"get_weather","arguments":""}}`, ""),
		toolChunk(`{"function":{"arguments":"{\"city\":"}}`, ""),
		toolChunk(`{"id":"a","function":{"arguments":"\"Paris\"}"}}`, "tool_calls"),
	)
	require.Len(t, calls, 1)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.Equal(t, `{"city":"Paris"}`, calls[0].Function.Arguments)
	assert.True(t, strings.HasPrefix(calls[0].ID, "a"))
}
