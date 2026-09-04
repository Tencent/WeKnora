package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesEndpoint(t *testing.T) {
	if got := responsesEndpoint("https://opencode.ai/zen/go/v1"); got != "https://opencode.ai/zen/go/v1/responses" {
		t.Errorf("got %q", got)
	}
	// No double-append when the base already ends with /responses.
	if got := responsesEndpoint("https://h/v1/responses"); got != "https://h/v1/responses" {
		t.Errorf("double append: got %q", got)
	}
}

func TestChatCompletionsEndpointGuard(t *testing.T) {
	if got := chatCompletionsEndpoint("https://h/v1"); got != "https://h/v1/chat/completions" {
		t.Errorf("got %q", got)
	}
	// Egress guard: stored full-path rows (e.g. deepseek-v4-flash) must not double-append.
	if got := chatCompletionsEndpoint("https://h/v1/chat/completions"); got != "https://h/v1/chat/completions" {
		t.Errorf("double append: got %q", got)
	}
}

func TestBuildResponsesInput(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "Be brief."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
		{Role: "user", Content: "Reply with exactly: proof-ok"},
	}
	in := buildResponsesInput(msgs)
	for _, want := range []string{"Be brief.", "Hi", "Hello", "proof-ok"} {
		if !strings.Contains(in, want) {
			t.Errorf("input missing %q:\n%s", want, in)
		}
	}
	if buildResponsesInput(nil) != "" {
		t.Error("nil messages should yield empty input")
	}
}

const responsesCompletedBody = `{"id":"resp_1","object":"response","status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"proof-ok"}]}],"usage":{"input_tokens":13,"output_tokens":265}}`

const responsesIncompleteBody = `{"id":"resp_2","object":"response","status":"incomplete","model":"m","output":[],"error":null,"usage":{"input_tokens":13,"output_tokens":20}}`

const responsesErrorBody = `{"error":{"message":"boom","type":"server_error"}}`

func TestParseResponsesBody(t *testing.T) {
	resp, err := parseResponsesBody([]byte(responsesCompletedBody))
	if err != nil {
		t.Fatalf("completed: %v", err)
	}
	if resp.Content != "proof-ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.Usage.PromptTokens != 13 || resp.Usage.CompletionTokens != 265 {
		t.Errorf("usage = %+v", resp.Usage)
	}

	// Envelope-valid but textless (reasoning ate the budget) must NOT error.
	resp, err = parseResponsesBody([]byte(responsesIncompleteBody))
	if err != nil {
		t.Fatalf("incomplete: %v", err)
	}
	if resp.Content != "" || resp.FinishReason != "incomplete" {
		t.Errorf("incomplete = %+v", resp)
	}

	if _, err = parseResponsesBody([]byte(responsesErrorBody)); err == nil {
		t.Error("error envelope should fail")
	}
	if _, err = parseResponsesBody([]byte(`{`)); err == nil {
		t.Error("malformed JSON should fail")
	}
}

// Stubbed end-to-end: Chat() against a fake /responses backend.
func TestResponsesChat_Stubbed(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()

	var capturedPath string
	var capturedRequest responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesCompletedBody))
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL,
		ModelName: "muse-spark-1.3-contributor",
		APIKey:    "test-key",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	require.Equal(t, provider.ProviderResponses, c.GetProvider())

	resp, err := c.Chat(context.Background(), []Message{
		{Role: "user", Content: "Reply with exactly: proof-ok"},
	}, &ChatOptions{MaxTokens: 300})
	require.NoError(t, err)
	assert.Equal(t, "/responses", capturedPath)
	assert.Equal(t, "muse-spark-1.3-contributor", capturedRequest.Model)
	assert.Equal(t, 300, capturedRequest.MaxOutputTokens)
	assert.Contains(t, capturedRequest.Input, "proof-ok")
	assert.Equal(t, "proof-ok", resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)
}

// Streaming is #18: ChatStream must fail loudly instead of posting a
// chat-completions body to /responses.
func TestResponsesChatStream_Rejected(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should reach the backend")
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL,
		ModelName: "m",
		APIKey:    "k",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	_, err = c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "streaming is not supported")
}
func TestResponsesChat_StubbedFullEndpointBaseURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesCompletedBody))
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL + "/v1/responses",
		ModelName: "m",
		APIKey:    "k",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	_, err = c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(capturedPath, "/responses"))
	assert.NotContains(t, capturedPath, "responses/responses")
}

func TestResolveResponsesEffort(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		want  string
	}{
		{"unset defaults medium", nil, "medium"},
		{"empty defaults medium", map[string]string{"reasoning_effort": ""}, "medium"},
		{"low passes through", map[string]string{"reasoning_effort": "low"}, "low"},
		{"none passes through", map[string]string{"reasoning_effort": "none"}, "none"},
		{"case and space normalized", map[string]string{"reasoning_effort": " High "}, "high"},
		{"unknown falls back medium", map[string]string{"reasoning_effort": "ultra"}, "medium"},
		{"xhigh not in v1 set falls back", map[string]string{"reasoning_effort": "xhigh"}, "medium"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveResponsesEffort(tc.extra); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Effort must ride every /responses request (default medium).
func TestResponsesChat_SendsEffort(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	var capturedRequest responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesCompletedBody))
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL,
		ModelName: "m",
		APIKey:    "k",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	_, err = c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	require.NotNil(t, capturedRequest.Reasoning)
	assert.Equal(t, "medium", capturedRequest.Reasoning.Effort)

	c2, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:     server.URL,
		ModelName:   "m",
		APIKey:      "k",
		Provider:    string(provider.ProviderResponses),
		ExtraConfig: map[string]string{"reasoning_effort": "low"},
	})
	require.NoError(t, err)
	_, err = c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	require.NotNil(t, capturedRequest.Reasoning)
	assert.Equal(t, "low", capturedRequest.Reasoning.Effort)
}

// Incomplete envelopes (reasoning ate the budget) must succeed at the chat
// layer so the connection test treats envelope-valid as success.
func TestResponsesChat_IncompleteSucceeds(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesIncompleteBody))
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL,
		ModelName: "m",
		APIKey:    "k",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, &ChatOptions{MaxTokens: 300})
	require.NoError(t, err)
	assert.Equal(t, "", resp.Content)
	assert.Equal(t, "incomplete", resp.FinishReason)
}
