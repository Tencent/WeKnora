package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type primeRPCRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"params"`
}

func primeTestServer(t *testing.T, call func(http.ResponseWriter, *http.Request, primeRPCRequest)) *httptest.Server {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request primeRPCRequest
		if r.Method == http.MethodPost {
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		call(w, r, request)
	}))
	t.Cleanup(server.Close)
	return server
}

func primeRPCReply(t *testing.T, w http.ResponseWriter, id json.RawMessage, result interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "result": result,
	}))
}

func TestZhipuPrimeSearchProtocolAndSessionIsolation(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	server := primeTestServer(t, func(w http.ResponseWriter, r *http.Request, req primeRPCRequest) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodDelete {
			assert.Contains(t, []string{"session-first", "session-second"}, r.Header.Get("Mcp-Session-Id"))
			methods = append(methods, "DELETE")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		auth := r.Header.Get("Authorization")
		assert.Contains(t, []string{"Bearer first", "Bearer second"}, auth)
		session := "session-" + strings.TrimPrefix(auth, "Bearer ")
		methods = append(methods, req.Method)
		if req.Method != "initialize" {
			assert.Equal(t, session, r.Header.Get("Mcp-Session-Id"))
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", session)
			primeRPCReply(t, w, req.ID, map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]string{"name": "prime-test", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			primeRPCReply(t, w, req.ID, map[string]interface{}{"tools": []interface{}{
				map[string]interface{}{"name": "webSearchPrime", "inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"search_query": map[string]string{"type": "string"}},
				}},
			}})
		case "tools/call":
			assert.Equal(t, "webSearchPrime", req.Params.Name)
			assert.Equal(t, map[string]interface{}{"search_query": "WeKnora 中文"}, req.Params.Arguments)
			payload, err := json.Marshal(`[{"title":"No URL"},{"title":"WeKnora","link":"https://example.com",` +
				`"content":"搜索摘要","publish_date":"2026-01-02"},{"title":"Extra","link":"https://example.org"}]`)
			assert.NoError(t, err)
			primeRPCReply(t, w, req.ID, map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": string(payload)}}, "isError": false,
			})
		default:
			t.Errorf("unexpected MCP method: %s", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	for _, key := range []string{"first", "second"} {
		provider := &ZhipuPrimeProvider{apiKey: key, endpoint: server.URL}
		results, err := provider.Search(context.Background(), "  WeKnora 中文  ", 1, key == "first")
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "WeKnora", results[0].Title)
		assert.Equal(t, "https://example.com", results[0].URL)
		assert.Equal(t, "搜索摘要", results[0].Snippet)
		assert.Equal(t, "zhipu_prime", results[0].Source)
		if key == "first" {
			require.NotNil(t, results[0].PublishedAt)
			assert.Equal(t, "2026-01-02", results[0].PublishedAt.Format("2006-01-02"))
		} else {
			assert.Nil(t, results[0].PublishedAt)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	sequence := []string{"initialize", "notifications/initialized", "tools/list", "tools/call", "DELETE"}
	assert.Equal(t, append(sequence, sequence...), methods)
}

func TestZhipuPrimeSearchFailureAndCancellation(t *testing.T) {
	for _, failure := range []string{"unauthorized", "rpc", "tool", "malformed", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			started := make(chan struct{})
			server := primeTestServer(t, func(w http.ResponseWriter, r *http.Request, req primeRPCRequest) {
				if failure == "unauthorized" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch req.Method {
				case "initialize":
					primeRPCReply(t, w, req.ID, map[string]interface{}{
						"protocolVersion": "2025-03-26", "capabilities": map[string]interface{}{},
						"serverInfo": map[string]string{"name": "prime-test", "version": "1"},
					})
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
				case "tools/list":
					primeRPCReply(t, w, req.ID, map[string]interface{}{"tools": []interface{}{
						map[string]interface{}{
							"name": "web_search_prime", "inputSchema": map[string]string{"type": "object"},
						},
					}})
				case "tools/call":
					if failure == "cancel" {
						close(started)
						<-r.Context().Done()
						return
					}
					if failure == "rpc" {
						w.Header().Set("Content-Type", "application/json")
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
							"jsonrpc": "2.0", "id": req.ID,
							"error": map[string]interface{}{"code": -32000, "message": "quota exceeded"},
						}))
						return
					}
					primeRPCReply(t, w, req.ID, map[string]interface{}{
						"content": []map[string]string{{"type": "text", "text": "upstream failure"}},
						"isError": failure == "tool",
					})
				}
			})
			provider := &ZhipuPrimeProvider{apiKey: "test", endpoint: server.URL}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancel" {
				done := make(chan error, 1)
				go func() { _, err := provider.Search(ctx, "query", 1, false); done <- err }()
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("search did not start")
				}
				cancel()
				select {
				case err := <-done:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(time.Second):
					t.Fatal("cancellation did not stop search")
				}
				return
			}
			_, err := provider.Search(ctx, "query", 1, false)
			require.Error(t, err)
		})
	}
}

func TestParseZhipuPrimeResults(t *testing.T) {
	array := `[{"title":"Result","link":"https://example.com","content":"summary"}]`
	encoded, err := json.Marshal(array)
	require.NoError(t, err)
	doubleEncoded, err := json.Marshal(string(encoded))
	require.NoError(t, err)
	for _, payload := range []string{array, string(encoded), string(doubleEncoded), `{"search_result":` + array + `}`} {
		results, err := parseZhipuPrimeResults(payload)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "summary", results[0].Content)
	}
	for _, payload := range []string{"[]", `"[]"`, `{"search_result":[]}`} {
		results, err := parseZhipuPrimeResults(payload)
		require.NoError(t, err)
		assert.Empty(t, results)
	}
	for _, payload := range []string{
		"", "null", "{}", "[broken", `{"error":{"message":"quota exceeded"}}`,
		strings.Repeat("x", maxZhipuResponseBytes+1),
	} {
		_, err := parseZhipuPrimeResults(payload)
		require.Error(t, err)
	}
}

func TestZhipuPrimeToolDiscoveryAndValidation(t *testing.T) {
	for _, name := range []string{"webSearchPrime", "web_search_prime"} {
		toolName, args, err := zhipuPrimeTool([]*types.MCPTool{{
			Name: name, InputSchema: json.RawMessage(`{"properties":{"count":{"type":"integer"}}}`),
		}}, "query", 5)
		require.NoError(t, err)
		assert.Equal(t, name, toolName)
		assert.Equal(t, map[string]interface{}{"search_query": "query", "count": 5}, args)
	}
	_, _, err := zhipuPrimeTool([]*types.MCPTool{{Name: "unrelated"}}, "query", 1)
	require.ErrorContains(t, err, "does not expose")
	for i, params := range []types.WebSearchProviderParameters{
		{}, {APIKey: " "}, {APIKey: "test", ProxyURL: "http://127.0.0.1:8080"},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, err := NewZhipuPrimeProvider(params)
			require.Error(t, err)
		})
	}
	provider, err := NewZhipuPrimeProvider(types.WebSearchProviderParameters{APIKey: "test"})
	require.NoError(t, err)
	assert.Equal(t, defaultZhipuPrimeURL, provider.(*ZhipuPrimeProvider).endpoint)
	_, err = provider.Search(context.Background(), " \t", 1, false)
	require.ErrorContains(t, err, "cannot be empty")
}
