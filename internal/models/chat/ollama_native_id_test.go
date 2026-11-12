package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	ollamaapi "github.com/ollama/ollama/api"
	"github.com/stretchr/testify/require"
)

func TestOllamaToolCallIDsSurviveAgentRounds(t *testing.T) {
	requests := make(chan ollamaapi.ChatRequest, 2)
	ids := []string{"call_first", "call_second"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/api/tags":
			_, _ = fmt.Fprint(w, `{"models":[{"name":"m:latest"}]}`)
		case "/api/chat":
			var req ollamaapi.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			id := ids[0]
			if len(req.Messages) > 1 {
				id = ids[1]
			}
			requests <- req
			_, _ = fmt.Fprintf(w, `{"message":{"role":"assistant","tool_calls":[`+
				`{"id":%q,"function":{"index":0,"name":"lookup","arguments":{}}}]},"done":false}`+"\n", id)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant"},"done":true,"done_reason":"stop"}`)
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
	messages := []Message{{Role: "user", Content: "look it up"}}
	got := make([]string, 0, 2)
	for range ids {
		stream, err := c.ChatStream(t.Context(), messages, &ChatOptions{})
		require.NoError(t, err)
		var toolCalls []types.LLMToolCall
		for frame := range stream {
			require.NotEqual(t, types.ResponseTypeError, frame.ResponseType, "%+v", frame)
			if len(frame.ToolCalls) > 0 {
				toolCalls = frame.ToolCalls
			}
		}
		require.Len(t, toolCalls, 1)
		call := toolCalls[0]
		got = append(got, call.ID)
		messages = append(messages,
			Message{Role: "assistant", ToolCalls: []ToolCall{
				{ID: call.ID, Type: call.Type, Function: FunctionCall{
					Name: call.Function.Name, Arguments: call.Function.Arguments,
				}},
			}},
			Message{Role: "tool", Name: call.Function.Name, ToolCallID: call.ID, Content: "lookup result"},
		)
	}
	require.Equal(t, ids, got)
	<-requests
	second := <-requests
	require.Len(t, second.Messages, 3)
	require.Equal(t, ids[0], second.Messages[1].ToolCalls[0].ID)
	require.Equal(t, ids[0], second.Messages[2].ToolCallID)
	require.Equal(t, "lookup", second.Messages[2].ToolName)
}

func TestOllamaToolCallKeepsLegacyNumericIndex(t *testing.T) {
	c := &OllamaChat{}
	calls := c.toolCallTo([]ollamaapi.ToolCall{{Function: ollamaapi.ToolCallFunction{Index: 7, Name: "lookup"}}})
	require.Len(t, calls, 1)
	require.Equal(t, "7", calls[0].ID)

	messages := c.convertMessages([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "7", Function: FunctionCall{Name: "lookup"}}}},
		{Role: "tool", Name: "lookup", ToolCallID: "7"},
	})
	require.Equal(t, 7, messages[0].ToolCalls[0].Function.Index)
	require.Empty(t, messages[0].ToolCalls[0].ID)
	require.Empty(t, messages[1].ToolCallID)
	require.Equal(t, "lookup", messages[1].ToolName)
}
