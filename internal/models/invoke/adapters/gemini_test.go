package adapters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wire-shape assertions for the Gemini OpenAI-compat adapter (裁定 #31):
// chat rides the official compat layer with the standard OpenAI vocabulary —
// reasoning_effort being the ONLY thinking surface (mechanism mapping is
// Google's server-side job; the native-era family dispatch is gone).

func geminiTestEndpoint() invoke.Endpoint {
	return invoke.Endpoint{
		BaseURL:     "https://generativelanguage.googleapis.com/v1beta/openai",
		Credentials: invoke.Credentials{APIKey: "g-key"},
	}
}

func decodeGeminiBody(t *testing.T, req *invoke.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &body))
	return body
}

func TestGeminiBuildChatRequestShape(t *testing.T) {
	a := newGeminiAdapter()
	opts := &invoke.ChatOptions{
		Messages:            []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")},
		MaxCompletionTokens: 256,
		Temperature:         0.7,
	}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash", opts)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", req.URL)
	assert.Equal(t, "Bearer g-key", req.Header.Get("Authorization"), "compat layer: standard Bearer")

	body := decodeGeminiBody(t, req)
	assert.Equal(t, "gemini-2.5-flash", body["model"])
	assert.Equal(t, float64(256), body["max_completion_tokens"])
	assert.Equal(t, float64(0.7), body["temperature"])
}

// Stored bases from the native era (or a pasted API root) gain the /openai
// segment; explicit /openai bases pass through; empty falls to the official
// compat endpoint.
func TestGeminiBaseURLNormalization(t *testing.T) {
	google := "https://generativelanguage.googleapis.com"
	cases := map[string]string{
		google + "/v1beta":          google + "/v1beta/openai",
		google + "/v1beta/":         google + "/v1beta/openai",
		google:                      google + "/openai",
		google + "/v1beta/openai":   google + "/v1beta/openai",
		"https://gw.example.com/v1": "https://gw.example.com/v1",
		"":                          "https://generativelanguage.googleapis.com/v1beta/openai",
	}
	for base, want := range cases {
		if got := geminiCompatBaseURL(base); got != want {
			t.Errorf("geminiCompatBaseURL(%q) = %q, want %q", base, got, want)
		}
	}
}

// The one thinking surface: reasoning_effort. off → "none", level → enum;
// nil thinking sends nothing. Mechanism mapping is server-side (Google).
func TestGeminiReasoningEffort(t *testing.T) {
	on := true
	off := false
	cases := []struct {
		name  string
		model string
		thnk  *bool
		lvl   string
		want  any // expected reasoning_effort; nil = field absent
	}{
		{"off maps none (flash)", "gemini-2.5-flash", &off, "", "none"},
		{"off maps none (pro — Google clamps or rejects server-side)", "gemini-2.5-pro", &off, "", "none"},
		{"off maps none (3-series)", "gemini-3-pro", &off, "", "none"},
		{"low", "gemini-2.5-flash", &on, "low", "low"},
		{"high", "gemini-2.5-flash", &on, "high", "high"},
		{"xhigh falls back per caps (gemini declares low/medium/high)", "gemini-2.5-flash", &on, "xhigh", "medium"},
		{"on without level resolves via caps (medium)", "gemini-2.5-flash", &on, "", "medium"},
		{"nil thinking sends nothing", "gemini-2.5-flash", nil, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newGeminiAdapter()
			opts := &invoke.ChatOptions{
				Thinking: tc.thnk, ThinkingLevel: tc.lvl,
				Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")},
			}
			req, err := a.BuildChatRequest(geminiTestEndpoint(), tc.model, opts)
			require.NoError(t, err)
			body := decodeGeminiBody(t, req)
			if tc.want == nil {
				assert.NotContains(t, body, "reasoning_effort")
				return
			}
			assert.Equal(t, tc.want, body["reasoning_effort"])
		})
	}
}

// Structured output rides the openai family's json_object + prompt-hint form
// (the compat layer is an OpenAI Chat surface — same handling as everywhere).
func TestGeminiFormatMapping(t *testing.T) {
	a := newGeminiAdapter()
	opts := &invoke.ChatOptions{
		Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")},
		Format:   json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}}}`),
	}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash", opts)
	require.NoError(t, err)
	body := decodeGeminiBody(t, req)
	rf := body["response_format"].(map[string]any)
	assert.Equal(t, "json_object", rf["type"])
	last := body["messages"].([]any)[len(body["messages"].([]any))-1].(map[string]any)
	raw, err := json.Marshal(last)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Use this JSON schema")
}

// Model listing targets the compat /models root directly (the family's
// openAIListURL would derive .../openai/v1/models — wrong for this base).
func TestGeminiListFacet(t *testing.T) {
	a := newGeminiAdapter()
	req, err := a.BuildListRequest(geminiTestEndpoint(), invoke.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/openai/models", req.URL)
	assert.Equal(t, "Bearer g-key", req.Header.Get("Authorization"))

	models, err := a.ParseListResponse(http.StatusOK, nil,
		[]byte(`{"data":[{"id":"gemini-2.5-flash","owned_by":"google"},{"id":"gemini-embedding"}]}`))
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "gemini-2.5-flash", models[0].ID)
}

// End-to-end stream over an openai-shaped SSE stub: the composed adapter
// rides the family bridge (text deltas, usage frame, done).
func TestGeminiChatStreamEndToEnd(t *testing.T) {
	allowLoopbackSSRF(t)
	sseBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hel"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		``,
		// Standard OpenAI usage frame (stream_options.include_usage), before [DONE].
		`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	ch, err := invoke.ChatStream(t.Context(), &invoke.ModelConfig{
		Provider: "gemini", ModelID: "m", ModelName: "gemini-2.5-flash", BaseURL: srv.URL,
		Credentials: invoke.Credentials{APIKey: "k"},
	}, &invoke.ChatOptions{Stream: true, Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")}})
	require.NoError(t, err)

	var text strings.Builder
	var finish string
	var done bool
	var usage *struct{ prompt, completion, total int }
	for sr := range ch {
		if sr.ResponseType == "answer" && sr.Content != "" {
			text.WriteString(sr.Content)
		}
		if sr.Usage != nil {
			usage = &struct{ prompt, completion, total int }{
				sr.Usage.PromptTokens, sr.Usage.CompletionTokens, sr.Usage.TotalTokens,
			}
		}
		if sr.Done {
			done = true
			finish = sr.FinishReason
		}
	}
	assert.True(t, done)
	assert.Equal(t, "hello", text.String())
	assert.Equal(t, "stop", finish)
	require.NotNil(t, usage)
	assert.Equal(t, 4, usage.prompt)
	assert.Equal(t, 2, usage.completion)
	assert.Equal(t, 6, usage.total)
}
