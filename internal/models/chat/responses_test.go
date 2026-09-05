package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
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
	resp, err := ParseResponsesBody([]byte(responsesCompletedBody))
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
	resp, err = ParseResponsesBody([]byte(responsesIncompleteBody))
	if err != nil {
		t.Fatalf("incomplete: %v", err)
	}
	if resp.Content != "" || resp.FinishReason != "incomplete" {
		t.Errorf("incomplete = %+v", resp)
	}

	if _, err = ParseResponsesBody([]byte(responsesErrorBody)); err == nil {
		t.Error("error envelope should fail")
	}
	if _, err = ParseResponsesBody([]byte(`{`)); err == nil {
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

// Stubbed end-to-end: ChatStream() assembles a live SSE transcript.
func TestResponsesChatStream_Stubbed(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesStreamTranscript))
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL,
		ModelName: "m",
		APIKey:    "k",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "/responses", capturedPath)
	var text strings.Builder
	var done *types.StreamResponse
	for e := range ch {
		if e.ResponseType == types.ResponseTypeAnswer && !e.Done {
			text.WriteString(e.Content)
		}
		if e.Done {
			d := e
			done = &d
		}
	}
	assert.Equal(t, "proof-ok", text.String())
	require.NotNil(t, done)
	require.NotNil(t, done.Usage)
	assert.Equal(t, 13, done.Usage.PromptTokens)
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

const responsesStreamTranscript = `data: {"type":"response.created","response":{"id":"resp_1"}}

data: {"type":"response.output_text.delta","delta":"proof"}

data: {"type":"response.output_text.delta","delta":"-ok"}

data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":13,"output_tokens":20}}}
`

const responsesStreamFailed = `data: {"type":"response.failed","response":{"error":{"message":"boom"}}}
`

const responsesStreamToolCall = `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city"}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\":\"Oslo\"}"}

data: {"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"city\":\"Oslo\"}"}

data: {"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":5,"output_tokens":8}}}
`

func collectResponsesStream(t *testing.T, transcript string) []types.StreamResponse {
	t.Helper()
	var out []types.StreamResponse
	err := runResponsesStream(strings.NewReader(transcript), func(sr types.StreamResponse) {
		out = append(out, sr)
	})
	require.NoError(t, err)
	return out
}

// Acceptance: stubbed SSE transcript assembles to the non-stream text.
func TestResponsesStream_TextAssembles(t *testing.T) {
	events := collectResponsesStream(t, responsesStreamTranscript)
	var text strings.Builder
	var done *types.StreamResponse
	for i, e := range events {
		if e.ResponseType == types.ResponseTypeAnswer && !e.Done {
			text.WriteString(e.Content)
		}
		if e.Done {
			d := e
			done = &d
		}
		_ = i
	}
	assert.Equal(t, "proof-ok", text.String())
	require.NotNil(t, done)
	require.NotNil(t, done.Usage)
	assert.Equal(t, 13, done.Usage.PromptTokens)
	assert.Equal(t, 20, done.Usage.CompletionTokens)
}

func TestResponsesStream_Failed(t *testing.T) {
	events := collectResponsesStream(t, responsesStreamFailed)
	require.Len(t, events, 1)
	assert.Equal(t, types.ResponseTypeError, events[0].ResponseType)
	assert.True(t, events[0].Done)
	assert.Contains(t, events[0].Content, "boom")
}

// Tool-call deltas must not break the stream; the assembled call surfaces.
func TestResponsesStream_ToolCallSurfaces(t *testing.T) {
	events := collectResponsesStream(t, responsesStreamToolCall)
	var calls []types.LLMToolCall
	var done *types.StreamResponse
	for _, e := range events {
		if e.ResponseType == types.ResponseTypeToolCall {
			calls = append(calls, e.ToolCalls...)
		}
		if e.Done {
			d := e
			done = &d
		}
	}
	require.Len(t, calls, 1)
	assert.Equal(t, "get_weather", calls[0].Function.Name)
	assert.JSONEq(t, `{"city":"Oslo"}`, calls[0].Function.Arguments)
	require.NotNil(t, done)
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
			if got := ResolveResponsesEffort(tc.extra); got != tc.want {
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

const responsesToolCallBody = `{"id":"resp_3","object":"response","status":"completed","model":"m","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}],"usage":{"input_tokens":20,"output_tokens":10}}`

func TestBuildResponsesTools(t *testing.T) {
	opts := &ChatOptions{
		Tools: []Tool{{Type: "function", Function: FunctionDef{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}}},
		ToolChoice: "auto",
	}
	tools, choice := buildResponsesTools(opts)
	require.Len(t, tools, 1)
	assert.Equal(t, "function", tools[0].Type)
	assert.Equal(t, "get_weather", tools[0].Name)
	assert.Equal(t, "Get weather", tools[0].Description)
	assert.JSONEq(t, `{"type":"object"}`, string(tools[0].Parameters))
	assert.Equal(t, "auto", choice)
	if tools, choice := buildResponsesTools(nil); len(tools) != 0 || choice != nil {
		t.Errorf("nil opts should yield no tools, got %v %v", tools, choice)
	}
}

func TestBuildResponsesInputValue_TextStaysString(t *testing.T) {
	v := BuildResponsesInputValue([]Message{{Role: "user", Content: "hi"}})
	s, ok := v.(string)
	require.True(t, ok, "text-only input must stay a string, got %T", v)
	assert.Contains(t, s, "hi")
}

func TestBuildResponsesInputValue_VisionAndTools(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "what is this?", MultiContent: []MessageContentPart{
			{Type: "text", Text: "what is this?"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "https://x/img.png"}},
		}},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Type: "function",
			Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Oslo"}`}}}},
		{Role: "tool", Content: "sunny", ToolCallID: "call_1", Name: "get_weather"},
	}
	v := BuildResponsesInputValue(msgs)
	items, ok := v.([]responsesInputItem)
	require.True(t, ok, "multimodal input must be an item array, got %T", v)
	require.Len(t, items, 3)
	assert.Equal(t, "input_text", items[0].Content[0].Type)
	assert.Equal(t, "input_image", items[0].Content[1].Type)
	assert.Equal(t, "https://x/img.png", items[0].Content[1].ImageURL)
	assert.Equal(t, "function_call", items[1].Type)
	assert.Equal(t, "get_weather", items[1].Name)
	assert.Equal(t, "function_call_output", items[2].Type)
	assert.Equal(t, "call_1", items[2].CallID)
	assert.Equal(t, "sunny", items[2].Output)
}

func TestParseResponsesBody_ToolCall(t *testing.T) {
	resp, err := ParseResponsesBody([]byte(responsesToolCallBody))
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_1", resp.ToolCalls[0].ID)
	assert.Equal(t, "function", resp.ToolCalls[0].Type)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"city":"Oslo"}`, resp.ToolCalls[0].Function.Arguments)
}

// Stubbed round-trip: tools go out, function_call comes back populated.
func TestResponsesChat_ToolRoundTrip(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	var capturedRequest responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesToolCallBody))
	}))
	defer server.Close()

	c, err := NewRemoteAPIChat(&ChatConfig{
		BaseURL:   server.URL,
		ModelName: "m",
		APIKey:    "k",
		Provider:  string(provider.ProviderResponses),
	})
	require.NoError(t, err)
	resp, err := c.Chat(context.Background(),
		[]Message{{Role: "user", Content: "weather in Oslo?"}},
		&ChatOptions{Tools: []Tool{{Type: "function", Function: FunctionDef{Name: "get_weather"}}}})
	require.NoError(t, err)
	require.Len(t, capturedRequest.Tools, 1)
	assert.Equal(t, "get_weather", capturedRequest.Tools[0].Name)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
}
