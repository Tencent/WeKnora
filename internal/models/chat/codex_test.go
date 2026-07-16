package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartCodexOAuthAuthorizeURL(t *testing.T) {
	oldEndpoint := codexAuthorizeEndpoint
	codexAuthorizeEndpoint = "https://auth.openai.test/oauth/authorize"
	t.Cleanup(func() { codexAuthorizeEndpoint = oldEndpoint })

	result, err := StartCodexOAuth(filepath.Join(t.TempDir(), "codex_auth.json"))
	require.NoError(t, err)
	require.Contains(t, result.AuthorizeURL, "scope=openid%20profile%20email%20offline_access")
	require.NotContains(t, result.AuthorizeURL, "scope=openid+profile")

	parsed, err := url.Parse(result.AuthorizeURL)
	require.NoError(t, err)
	q := parsed.Query()
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, codexOAuthClientID(), q.Get("client_id"))
	assert.Equal(t, codexOAuthRedirectURI, q.Get("redirect_uri"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, "true", q.Get("id_token_add_organizations"))
	assert.Equal(t, "true", q.Get("codex_cli_simplified_flow"))
	assert.Equal(t, "codex_cli_rs", q.Get("originator"))
	assert.Len(t, q.Get("state"), 32)
	assert.NotEmpty(t, q.Get("code_challenge"))
}

func TestStartCodexOAuthSweepsExpiredPendingStates(t *testing.T) {
	codexPendingOAuthMu.Lock()
	codexPendingOAuth = map[string]codexPendingOAuthState{
		"expired": {CodeVerifier: "old", CreatedAt: time.Now().Add(-codexPendingOAuthTTL - time.Minute)},
	}
	codexPendingOAuthMu.Unlock()
	t.Cleanup(func() {
		codexPendingOAuthMu.Lock()
		codexPendingOAuth = map[string]codexPendingOAuthState{}
		codexPendingOAuthMu.Unlock()
	})

	_, err := StartCodexOAuth(filepath.Join(t.TempDir(), "codex_auth.json"))
	require.NoError(t, err)

	codexPendingOAuthMu.Lock()
	_, expiredExists := codexPendingOAuth["expired"]
	pendingCount := len(codexPendingOAuth)
	codexPendingOAuthMu.Unlock()
	assert.False(t, expiredExists)
	assert.Equal(t, 1, pendingCount)
}

func TestCodexTokenRefreshRotatesAndPersists(t *testing.T) {
	now := time.Now()
	authFile := filepath.Join(t.TempDir(), "codex_auth.json")
	old := &codexTokenFile{
		Tokens: codexTokens{
			IDToken:      makeTestJWT(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_old"}}),
			AccessToken:  makeTestJWT(map[string]any{"exp": now.Add(time.Minute).Unix()}),
			RefreshToken: "refresh-old",
			AccountID:    "acct_old",
		},
		LastRefresh: now.Add(-time.Hour),
	}
	require.NoError(t, writeCodexTokenFile(authFile, old))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "refresh_token", body["grant_type"])
		assert.Equal(t, "refresh-old", body["refresh_token"])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codexTokenResponse{
			IDToken:      makeTestJWT(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_new"}}),
			AccessToken:  makeTestJWT(map[string]any{"exp": now.Add(time.Hour).Unix()}),
			RefreshToken: "refresh-new",
		})
	}))
	defer server.Close()

	oldEndpoint := codexTokenEndpoint
	codexTokenEndpoint = server.URL
	t.Cleanup(func() { codexTokenEndpoint = oldEndpoint })

	source := &codexTokenSource{authFile: authFile, client: server.Client()}
	accessToken, accountID, err := source.bearer(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "acct_new", accountID)
	assert.NotEmpty(t, accessToken)

	updated, err := readCodexTokenFile(authFile)
	require.NoError(t, err)
	assert.Equal(t, "refresh-new", updated.Tokens.RefreshToken)
	assert.Equal(t, "acct_new", updated.Tokens.AccountID)
	info, err := os.Stat(authFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(codexOAuthFilePermission), info.Mode().Perm())
}

func TestCodexTokenRefreshDecodesObjectError(t *testing.T) {
	now := time.Now()
	authFile := filepath.Join(t.TempDir(), "codex_auth.json")
	old := &codexTokenFile{
		Tokens: codexTokens{
			IDToken:      makeTestJWT(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_old"}}),
			AccessToken:  makeTestJWT(map[string]any{"exp": now.Add(time.Minute).Unix()}),
			RefreshToken: "refresh-old",
			AccountID:    "acct_old",
		},
		LastRefresh: now.Add(-time.Hour),
	}
	require.NoError(t, writeCodexTokenFile(authFile, old))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"refresh token expired","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	oldEndpoint := codexTokenEndpoint
	codexTokenEndpoint = server.URL
	t.Cleanup(func() { codexTokenEndpoint = oldEndpoint })

	source := &codexTokenSource{authFile: authFile, client: server.Client()}
	_, _, err := source.bearer(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh token expired")
}

func TestCodexTokenSourceUsesCachedCredentials(t *testing.T) {
	now := time.Now()
	authFile := filepath.Join(t.TempDir(), "codex_auth.json")
	file := &codexTokenFile{
		Tokens: codexTokens{
			IDToken:      makeTestJWT(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_cached"}}),
			AccessToken:  makeTestJWT(map[string]any{"exp": now.Add(time.Hour).Unix()}),
			RefreshToken: "refresh-cached",
			AccountID:    "acct_cached",
		},
		LastRefresh: now,
	}
	require.NoError(t, writeCodexTokenFile(authFile, file))

	source := &codexTokenSource{authFile: authFile, client: http.DefaultClient}
	accessToken, accountID, err := source.bearer(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, accessToken)
	assert.Equal(t, "acct_cached", accountID)

	require.NoError(t, os.Remove(authFile))
	accessToken, accountID, err = source.bearer(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, accessToken)
	assert.Equal(t, "acct_cached", accountID)
}

func TestShouldRefreshCodexTokens(t *testing.T) {
	now := time.Now()
	file := &codexTokenFile{
		Tokens: codexTokens{
			AccessToken: makeTestJWT(map[string]any{"exp": now.Add(4 * time.Minute).Unix()}),
		},
		LastRefresh: now,
	}
	assert.True(t, shouldRefreshCodexTokens(file, now))

	file.Tokens.AccessToken = makeTestJWT(map[string]any{"exp": now.Add(time.Hour).Unix()})
	file.LastRefresh = now.Add(-56 * time.Minute)
	assert.True(t, shouldRefreshCodexTokens(file, now))

	file.LastRefresh = now.Add(-10 * time.Minute)
	assert.False(t, shouldRefreshCodexTokens(file, now))
}

func TestBuildCodexResponsesRequest(t *testing.T) {
	thinking := true
	req := buildCodexResponsesRequest("gpt-5-codex", []Message{
		{Role: "system", Content: "You are concise."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}, &ChatOptions{
		Thinking: &thinking,
		Format:   json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	}, true)

	assert.Equal(t, "gpt-5-codex", req.Model)
	assert.Equal(t, "You are concise.", req.Instructions)
	require.Len(t, req.Input, 2)
	assert.Equal(t, "user", req.Input[0].Role)
	assert.Equal(t, "assistant", req.Input[1].Role)
	assert.True(t, req.Stream)
	assert.False(t, req.Store)
	require.NotNil(t, req.Reasoning)
	assert.Equal(t, "medium", req.Reasoning.Effort)
	require.NotNil(t, req.Text)
	format, ok := req.Text.Format.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "json_schema", format["type"])
}

func TestBuildCodexResponsesRequestRequiresInstructions(t *testing.T) {
	req := buildCodexResponsesRequest("gpt-5-codex", []Message{
		{Role: "user", Content: "Hello"},
	}, nil, true)

	assert.Equal(t, "You are a helpful assistant.", req.Instructions)
}

func TestBuildCodexResponsesRequestToolsAndToolOutputs(t *testing.T) {
	req := buildCodexResponsesRequest("gpt-5-codex", []Message{
		{Role: "user", Content: "Search"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: FunctionCall{
				Name:      "wiki_search",
				Arguments: `{"query":"retention"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_1", Name: "wiki_search", Content: `{"results":[{"title":"doc"}]}`},
	}, &ChatOptions{Tools: []Tool{{
		Type: "function",
		Function: FunctionDef{
			Name:        "wiki_search",
			Description: "Search wiki",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}}}, true)

	require.Len(t, req.Tools, 1)
	assert.Equal(t, "function", req.Tools[0].Type)
	assert.Equal(t, "wiki_search", req.Tools[0].Name)
	require.Len(t, req.Input, 3)
	assert.Equal(t, "user", req.Input[0].Role)
	assert.Equal(t, "function_call", req.Input[1].Type)
	assert.Equal(t, "call_1", req.Input[1].CallID)
	assert.Equal(t, "wiki_search", req.Input[1].Name)
	assert.Equal(t, `{"query":"retention"}`, req.Input[1].Arguments)
	assert.Equal(t, "function_call_output", req.Input[2].Type)
	assert.Equal(t, "call_1", req.Input[2].CallID)
	assert.Contains(t, req.Input[2].Output, "results")
}

func TestParseCodexResponsesSSE(t *testing.T) {
	sample := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"hello "}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"world"}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7,"input_tokens_details":{"cached_tokens":2}}}}`,
		"",
	}, "\n")

	resp, err := parseCodexResponsesSSE(strings.NewReader(sample))
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp.Content)
	assert.Equal(t, "thinking", resp.ReasoningContent)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Equal(t, 3, resp.Usage.PromptTokens)
	assert.Equal(t, 4, resp.Usage.CompletionTokens)
	assert.Equal(t, 7, resp.Usage.TotalTokens)
	assert.Equal(t, 2, resp.Usage.CachedTokens)
}

func TestParseCodexResponsesSSEToolCall(t *testing.T) {
	sample := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_abc","name":"wiki_search","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"query\""}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":":\"retention\"}"}`,
		"",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	resp, err := parseCodexResponsesSSE(strings.NewReader(sample))
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_abc", resp.ToolCalls[0].ID)
	assert.Equal(t, "function", resp.ToolCalls[0].Type)
	assert.Equal(t, "wiki_search", resp.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"query":"retention"}`, resp.ToolCalls[0].Function.Arguments)
}

func TestParseCodexResponsesSSEToolCallWithNonZeroOutputIndex(t *testing.T) {
	sample := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_abc","name":"wiki_search","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"query\":\"retention\"}"}`,
		"",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	resp, err := parseCodexResponsesSSE(strings.NewReader(sample))
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_abc", resp.ToolCalls[0].ID)
	assert.Equal(t, "wiki_search", resp.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"query":"retention"}`, resp.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", resp.FinishReason)
}

func TestParseCodexResponsesSSEToolCallsWithoutOutputIndex(t *testing.T) {
	sample := strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_a","name":"wiki_search","arguments":""}}`,
		"",
		`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_b","name":"wiki_read_page","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","call_id":"call_a","delta":"{\"query\":\"retention\"}"}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","call_id":"call_b","delta":"{\"slug\":\"index\"}"}`,
		"",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	resp, err := parseCodexResponsesSSE(strings.NewReader(sample))
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 2)
	assert.Equal(t, "call_a", resp.ToolCalls[0].ID)
	assert.Equal(t, "wiki_search", resp.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"query":"retention"}`, resp.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "call_b", resp.ToolCalls[1].ID)
	assert.Equal(t, "wiki_read_page", resp.ToolCalls[1].Function.Name)
	assert.JSONEq(t, `{"slug":"index"}`, resp.ToolCalls[1].Function.Arguments)
	assert.Equal(t, "tool_calls", resp.FinishReason)
}

func TestParseCodexResponsesSSEIgnoresArrayContentOutputItem(t *testing.T) {
	sample := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		"",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	resp, err := parseCodexResponsesSSE(strings.NewReader(sample))
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
	assert.Empty(t, resp.ToolCalls)
	assert.Equal(t, "stop", resp.FinishReason)
}

func TestBuildCodexResponsesRequestParallelToolCalls(t *testing.T) {
	parallel := true
	req := buildCodexResponsesRequest("gpt-5-codex", []Message{
		{Role: "user", Content: "Search"},
	}, &ChatOptions{ParallelToolCalls: &parallel}, true)

	require.NotNil(t, req.ParallelToolCalls)
	assert.True(t, *req.ParallelToolCalls)
}

func makeTestJWT(payload map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(body) + "."
}
