package googlegenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatPreservesNumbersInToolHistory(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const arguments = `{"id":9007199254740993}`
	const result = `[{"id":9223372036854775807}]`
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Done"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, nil)
	_, err := client.Chat(context.Background(), []api.Message{
		{Role: "user", Content: "Look up the order"},
		{Role: "assistant", ToolCalls: []api.ToolCall{{
			ID: "call_1", Type: "function", Function: api.FunctionCall{Name: "lookup_order", Arguments: arguments},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: result},
	}, nil)
	require.NoError(t, err)
	var body struct {
		Contents []struct {
			Parts []struct {
				FunctionCall struct {
					Args json.RawMessage
				}
				FunctionResponse struct {
					Response struct {
						Result json.RawMessage
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(<-requests, &body))
	require.Len(t, body.Contents, 3)
	require.Len(t, body.Contents[1].Parts, 1)
	require.Len(t, body.Contents[2].Parts, 1)
	assert.Equal(t, arguments, string(body.Contents[1].Parts[0].FunctionCall.Args))
	assert.Equal(t, result, string(body.Contents[2].Parts[0].FunctionResponse.Response.Result))
}

func TestToolJSONKeepsFallbacks(t *testing.T) {
	for _, input := range []string{"", "null", `{"id":1} {}`, `{"id":1} text`, `[1]`, `"text"`} {
		assert.Equal(t, map[string]any{}, parseArgs(input), "arguments %q", input)
	}
	for _, input := range []string{"", "null", `42`, `{"id":1} {}`, `[1] text`, `{"not-json"}`} {
		assert.Equal(t, input, decodeToolResult(input), "result %q", input)
	}
}
