package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestOllamaChatStreamRetainsToolCallsAcrossFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/api/tags":
			_, _ = fmt.Fprint(w, `{"models":[{"name":"m:latest"}]}`)
		case "/api/chat":
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","tool_calls":[`+
				`{"function":{"index":0,"name":"first","arguments":{"id":1}}}]},"done":false}`)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","tool_calls":[`+
				`{"function":{"index":1,"name":"second","arguments":{"id":2}}}]},"done":false}`)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant"},"done":true,"done_reason":"stop","eval_count":3}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("OLLAMA_OPTIONAL", "")
	svc, err := ollama.GetOllamaService()
	require.NoError(t, err)

	c := &OllamaChat{modelName: "m", ollamaService: svc}
	stream, err := c.ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, &ChatOptions{})
	require.NoError(t, err)
	var last types.StreamResponse
	var toolCalls []types.LLMToolCall
	for frame := range stream {
		require.NotEqual(t, types.ResponseTypeError, frame.ResponseType, "%+v", frame)
		last = frame
		// The Agent retains the last nonempty tool-call snapshot.
		if len(frame.ToolCalls) > 0 {
			toolCalls = frame.ToolCalls
		}
	}
	require.True(t, last.Done)
	require.Equal(t, "stop", last.FinishReason)
	require.Len(t, toolCalls, 2)
	require.Equal(t, "first", toolCalls[0].Function.Name)
	require.JSONEq(t, `{"id":1}`, toolCalls[0].Function.Arguments)
	require.Equal(t, "second", toolCalls[1].Function.Name)
	require.JSONEq(t, `{"id":2}`, toolCalls[1].Function.Arguments)
	require.NotEqual(t, toolCalls[0].ID, toolCalls[1].ID)
}
