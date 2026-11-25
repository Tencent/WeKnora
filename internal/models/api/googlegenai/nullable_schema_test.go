package googlegenai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBodyNullableToolSchema(t *testing.T) {
	for _, tc := range []struct {
		name, schema, want string
	}{
		{
			name:   "nullable string",
			schema: `{"type":"object","properties":{"query":{"type":["string","null"]}}}`,
			want:   `{"type":"object","properties":{"query":{"type":"string","nullable":true}}}`,
		},
		{
			name:   "null first",
			schema: `{"type":"object","properties":{"limit":{"type":["null","integer"],"minimum":1}}}`,
			want:   `{"type":"object","properties":{"limit":{"type":"integer","nullable":true,"minimum":1}}}`,
		},
		{
			name: "nested objects and array items",
			schema: `{"type":"object","required":["filters"],"properties":{"filters":{
				"type":["object","null"],"properties":{"tags":{"type":["array","null"],
				"items":{"type":["string","null"],"description":"tag"}}}}}}`,
			want: `{"type":"object","required":["filters"],"properties":{"filters":{
				"type":"object","nullable":true,"properties":{"tags":{"type":"array","nullable":true,
				"items":{"type":"string","nullable":true,"description":"tag"}}}}}}`,
		},
		{
			name: "schema keyword property names",
			schema: `{"type":"object","properties":{"type":{"type":["string","null"]},
				"nullable":{"type":["boolean","null"]},"title":{"type":["number","null"]}}}`,
			want: `{"type":"object","properties":{"type":{"type":"string","nullable":true},
				"nullable":{"type":"boolean","nullable":true},"title":{"type":"number","nullable":true}}}`,
		},
		{
			name:   "type union includes null despite nullable false",
			schema: `{"type":"object","properties":{"q":{"type":["string","null"],"nullable":false}}}`,
			want:   `{"type":"object","properties":{"q":{"type":"string","nullable":true}}}`,
		},
		{
			name: "anyOf branches",
			schema: `{"type":"object","properties":{"q":{"anyOf":[
				{"type":["string","null"]},{"type":"integer"}]}}}`,
			want: `{"type":"object","properties":{"q":{"anyOf":[
				{"type":"string","nullable":true},{"type":"integer"}]}}}`,
		},
		{
			name: "ordinary schema",
			schema: `{"type":"object","properties":{"q":{"type":"string","enum":["null","text"]},
				"flag":{"type":"boolean","nullable":false},"nothing":{"type":"null"}}}`,
			want: `{"type":"object","properties":{"q":{"type":"string","enum":["null","text"]},
				"flag":{"type":"boolean","nullable":false},"nothing":{"type":"null"}}}`,
		},
		{
			name:   "multiple concrete types retain their union",
			schema: `{"type":"object","properties":{"q":{"type":["string","integer","null"]}}}`,
			want:   `{"type":"object","properties":{"q":{"type":["string","integer","null"]}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, "https://example.invalid/v1beta", nil)
			parameters := json.RawMessage(tc.schema)
			body, err := client.BuildRequestBody([]api.Message{{Role: "user", Content: "search"}},
				&api.Options{Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{
					Name: "search", Parameters: parameters,
				}}}}, false)
			require.NoError(t, err)
			tools := body["tools"].([]map[string]any)
			declarations := tools[0]["functionDeclarations"].([]map[string]any)
			raw, err := json.Marshal(declarations[0]["parameters"])
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(raw))
			assert.Equal(t, tc.schema, string(parameters))
		})
	}
}

// Decode the wire request into the scalar type/nullable shape of Gemini's
// Schema. The old array-valued type fails here before a response is generated.
func TestNullableToolSchemaReachesGeminiHTTPRequests(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, stream := range []bool{false, true} {
		name := "Chat"
		if stream {
			name = "ChatStream"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Tools []struct {
						FunctionDeclarations []struct {
							Parameters struct {
								Properties map[string]struct {
									Type     string `json:"type"`
									Nullable bool   `json:"nullable"`
								} `json:"properties"`
							} `json:"parameters"`
						} `json:"functionDeclarations"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, `{"error":{"message":"type must be a scalar"}}`, http.StatusBadRequest)
					return
				}
				if len(request.Tools) != 1 || len(request.Tools[0].FunctionDeclarations) != 1 {
					http.Error(w, "missing tool declaration", http.StatusBadRequest)
					return
				}
				query := request.Tools[0].FunctionDeclarations[0].Parameters.Properties["query"]
				if query.Type != "string" || !query.Nullable {
					http.Error(w, "missing nullable string parameter", http.StatusBadRequest)
					return
				}
				response := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: "+response+"\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server.URL, nil)
			messages := []api.Message{{Role: "user", Content: "search"}}
			opts := &api.Options{Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{
				Name: "search", Parameters: json.RawMessage(`{
					"type":"object","properties":{"query":{"type":["string","null"]}}}`),
			}}}}
			if stream {
				chunks, err := client.ChatStream(t.Context(), messages, opts)
				require.NoError(t, err)
				var last types.StreamResponse
				var content string
				for chunk := range chunks {
					assert.NotEqual(t, types.ResponseTypeError, chunk.ResponseType, chunk.Content)
					content += chunk.Content
					last = chunk
				}
				assert.Equal(t, "ok", content)
				assert.True(t, last.Done)
				assert.Equal(t, "stop", last.FinishReason)
				return
			}
			response, err := client.Chat(t.Context(), messages, opts)
			require.NoError(t, err)
			assert.Equal(t, "ok", response.Content)
		})
	}
}
