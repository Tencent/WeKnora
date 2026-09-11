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

// Wire-shape assertions for the native generateContent adapter (P5-1). Every
// field pins a documented v1beta behavior — the source of truth is
// 模型服务商接口文档/Google Gemini/Generating content.md.

func geminiTestEndpoint() invoke.Endpoint {
	return invoke.Endpoint{
		BaseURL:     "https://generativelanguage.googleapis.com/v1beta",
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
	a := &GeminiAdapter{}
	opts := &invoke.ChatOptions{
		Messages: []invoke.Message{
			invoke.TextMessage(invoke.RoleSystem, "be brief"),
			invoke.TextMessage(invoke.RoleUser, "hi"),
		},
		MaxCompletionTokens: 256,
		Temperature:         0.7,
	}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash", opts)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, req.Method)
	const wantURL = "https://generativelanguage.googleapis.com/v1beta" +
		"/models/gemini-2.5-flash:generateContent"
	assert.Equal(t, wantURL, req.URL)
	assert.Equal(t, "g-key", req.Header.Get("X-Goog-Api-Key"))
	assert.Empty(t, req.Header.Get("Authorization"), "key rides the header, never a Bearer or the URL")

	body := decodeGeminiBody(t, req)
	sysParts := mustMap(t, body, "systemInstruction").(map[string]any)["parts"].([]any)
	assert.Equal(t, "be brief", sysParts[0].(map[string]any)["text"])
	contents := body["contents"].([]any)
	assert.Len(t, contents, 1)
	assert.Equal(t, "user", contents[0].(map[string]any)["role"])
	gc := body["generationConfig"].(map[string]any)
	assert.Equal(t, float64(256), gc["maxOutputTokens"])
	assert.Equal(t, float64(0.7), gc["temperature"])
}

func TestGeminiStreamURL(t *testing.T) {
	a := &GeminiAdapter{}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash",
		&invoke.ChatOptions{Stream: true, Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")}})
	require.NoError(t, err)
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		req.URL)
	assert.True(t, req.Stream)
}

// The v1 dual-URL trap (chat configured on the compat layer) must not leak
// into the native route.
func TestGeminiBaseURLCompatSuffixStripped(t *testing.T) {
	a := &GeminiAdapter{}
	ep := geminiTestEndpoint()
	ep.BaseURL = "https://proxy.example.com/v1beta/openai/"
	req, err := a.BuildChatRequest(ep, "m",
		&invoke.ChatOptions{Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")}})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(req.URL, "https://proxy.example.com/v1beta/models/"), req.URL)
}

func TestGeminiToolRoundTrip(t *testing.T) {
	a := &GeminiAdapter{}
	opts := &invoke.ChatOptions{
		Messages: []invoke.Message{
			invoke.TextMessage(invoke.RoleUser, "weather?"),
			{Role: invoke.RoleAssistant, ToolCalls: []invoke.ToolCall{{
				ID: "call-1", Type: "function",
				Function: invoke.FunctionCall{Name: "get_weather", Arguments: `{"city":"sf"}`},
			}}},
			{Role: invoke.RoleTool, ToolCallID: "call-1", Content: []invoke.Part{{Text: "sunny"}}},
		},
		Tools: []invoke.ToolDef{{
			Name: "get_weather", Description: "w",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: "get_weather",
	}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash", opts)
	require.NoError(t, err)
	body := decodeGeminiBody(t, req)

	// user turn, model turn with functionCall, user turn with functionResponse
	contents := body["contents"].([]any)
	require.Len(t, contents, 3)
	modelTurn := contents[1].(map[string]any)
	assert.Equal(t, "model", modelTurn["role"])
	call := modelTurn["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	assert.Equal(t, "get_weather", call["name"])
	assert.Equal(t, "call-1", call["id"])

	toolTurn := contents[2].(map[string]any)
	resp := toolTurn["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	assert.Equal(t, "get_weather", resp["name"], "name recovered from the issuing assistant turn")
	assert.JSONEq(t, `{"output":"sunny"}`, stringify(t, resp["response"]))

	tools := body["tools"].([]any)
	decl := tools[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	assert.Equal(t, "get_weather", decl["name"])
	fcc := mustMap(t, body, "toolConfig").(map[string]any)["functionCallingConfig"].(map[string]any)
	assert.Equal(t, "ANY", fcc["mode"])
	assert.Equal(t, []any{"get_weather"}, fcc["allowedFunctionNames"])
}

func TestGeminiImageParts(t *testing.T) {
	a := &GeminiAdapter{}
	opts := &invoke.ChatOptions{Messages: []invoke.Message{{
		Role: invoke.RoleUser,
		Content: []invoke.Part{
			{Text: "what is this"},
			{Image: &invoke.ImageRef{URL: "data:image/png;base64,AAAA"}},
			{Image: &invoke.ImageRef{URL: "https://example.com/pic.png"}},
		},
	}}}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash", opts)
	require.NoError(t, err)
	parts := decodeGeminiBody(t, req)["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 3)
	inline := parts[1].(map[string]any)["inlineData"].(map[string]any)
	assert.Equal(t, "image/png", inline["mimeType"])
	assert.Equal(t, "AAAA", inline["data"])
	file := parts[2].(map[string]any)["fileData"].(map[string]any)
	assert.Equal(t, "https://example.com/pic.png", file["fileUri"])
}

// TestGeminiThinkingByFamily pins the R1 fix: three model families, three
// incompatible mechanisms — the same user decision must produce the wire
// shape each family actually accepts (budget:0 on 2.5-pro / budget on 3.x
// were the 400s the first cut shipped).
func TestGeminiThinkingByFamily(t *testing.T) {
	on := true
	off := false
	type want struct {
		thinkingConfig map[string]any
		absent         bool // no generationConfig at all
	}
	cases := []struct {
		name  string
		model string
		thnk  *bool
		lvl   string
		want  want
	}{
		// gemini-3 family: thinkingLevel enum; "off" lands on the LOW floor.
		{
			"3-pro on high", "gemini-3-pro", &on, "high",
			want{thinkingConfig: map[string]any{"thinkingLevel": "HIGH", "includeThoughts": true}},
		},
		{
			"3-pro off floors at LOW", "gemini-3-pro", &off, "",
			want{thinkingConfig: map[string]any{"thinkingLevel": "LOW"}},
		},
		{
			"3-flash on low", "gemini-3-flash", &on, "low",
			want{thinkingConfig: map[string]any{"thinkingLevel": "LOW", "includeThoughts": true}},
		},
		// 2.5-flash family: budget mechanism; 0 is the documented off switch.
		{
			"2.5-flash off sends budget zero", "gemini-2.5-flash", &off, "",
			want{thinkingConfig: map[string]any{"thinkingBudget": float64(0)}},
		},
		{
			"2.5-flash low budget", "gemini-2.5-flash", &on, "low",
			want{thinkingConfig: map[string]any{"thinkingBudget": float64(1024), "includeThoughts": true}},
		},
		{
			"2.5-flash high budget", "gemini-2.5-flash", &on, "high",
			want{thinkingConfig: map[string]any{"thinkingBudget": float64(24576), "includeThoughts": true}},
		},
		// 2.5-pro: cannot be disabled, no level mechanism — never send anything.
		{"2.5-pro off sends nothing", "gemini-2.5-pro", &off, "", want{absent: true}},
		{"2.5-pro on sends nothing (default on)", "gemini-2.5-pro", &on, "high", want{absent: true}},
		// Unknown family: safe default, never a 400.
		{"unknown model sends nothing", "gemini-2.0-flash", &off, "", want{absent: true}},
		{"nil thinking sends nothing", "gemini-3-pro", nil, "", want{absent: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &GeminiAdapter{}
			opts := &invoke.ChatOptions{
				Thinking: tc.thnk, ThinkingLevel: tc.lvl,
				Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")},
			}
			req, err := a.BuildChatRequest(geminiTestEndpoint(), tc.model, opts)
			require.NoError(t, err)
			body := decodeGeminiBody(t, req)
			gc, ok := body["generationConfig"].(map[string]any)
			if tc.want.absent {
				assert.False(t, ok, "no generationConfig expected")
				return
			}
			require.True(t, ok)
			assert.Equal(t, tc.want.thinkingConfig, gc["thinkingConfig"])
		})
	}
}

// P1-2: structured output maps onto the native schema channel (no prompt-side
// hint like the compat layer needed).
func TestGeminiFormatMapping(t *testing.T) {
	a := &GeminiAdapter{}
	opts := &invoke.ChatOptions{
		Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")},
		Format:   json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}}}`),
	}
	req, err := a.BuildChatRequest(geminiTestEndpoint(), "gemini-2.5-flash", opts)
	require.NoError(t, err)
	gc := decodeGeminiBody(t, req)["generationConfig"].(map[string]any)
	assert.Equal(t, "application/json", gc["responseMimeType"])
	assert.JSONEq(t, `{"type":"object","properties":{"x":{"type":"number"}}}`,
		stringify(t, gc["responseJsonSchema"]))
}

func TestGeminiParseChatResponse(t *testing.T) {
	a := &GeminiAdapter{}
	body := `{"candidates":[{"content":{"role":"model","parts":[
		{"text":"answer "},{"functionCall":{"name":"f","args":{"x":1}},"thought":false}
	]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":3,"totalTokenCount":18}}`
	resp, err := a.ParseChatResponse(http.StatusOK, nil, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, "answer ", resp.Content)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "gemini_call_1", resp.ToolCalls[0].ID, "synthesized when the vendor omits id")
	assert.Equal(t, `{"x":1}`, resp.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", resp.FinishReason, "tool calls outrank the stop reason")
	assert.Equal(t, 10, resp.Usage.PromptTokens)
	assert.Equal(t, 8, resp.Usage.CompletionTokens, "candidates + thoughts (budget carved from output)")
	assert.Equal(t, 18, resp.Usage.TotalTokens)

	maxTok := `{"candidates":[{"content":{"parts":[{"text":"cut"}]},"finishReason":"MAX_TOKENS"}]}`
	resp, err = a.ParseChatResponse(http.StatusOK, nil, []byte(maxTok))
	require.NoError(t, err)
	assert.Equal(t, "length", resp.FinishReason)

	blocked := `{"promptFeedback":{"blockReason":"SAFETY"}}`
	_, err = a.ParseChatResponse(http.StatusOK, nil, []byte(blocked))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt blocked")
}

func TestGeminiListFacet(t *testing.T) {
	a := &GeminiAdapter{}
	req, err := a.BuildListRequest(geminiTestEndpoint())
	require.NoError(t, err)
	assert.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000",
		req.URL)
	assert.Equal(t, "g-key", req.Header.Get("X-Goog-Api-Key"))

	listBody := `{"models":[{"name":"models/gemini-2.5-flash",` +
		`"displayName":"Gemini 2.5 Flash"},{"name":"models/embedding"}]}`
	models, err := a.ParseListResponse(http.StatusOK, nil, []byte(listBody))
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "gemini-2.5-flash", models[0].ID, "models/ prefix stripped")
	assert.Equal(t, "Gemini 2.5 Flash", models[0].DisplayName)
}

func mustMap(t *testing.T, m map[string]any, key string) any {
	t.Helper()
	v, ok := m[key]
	require.True(t, ok, "key %q missing", key)
	return v
}

func stringify(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestGeminiChatStreamEndToEnd pins the full stream path over a stub SSE body:
// text deltas, a whole-call functionCall fragment, and the terminator frame
// carrying finishReason + usage — the usage-in-Done seam must surface it.
func TestGeminiChatStreamEndToEnd(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"hel"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[` +
			`{"functionCall":{"name":"f","args":{"a":1}}}]},"finishReason":"STOP"}],` +
			`"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,` +
			`"thoughtsTokenCount":0,"totalTokenCount":6}}`,
		``,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	allowLoopbackSSRF(t)

	ch, err := invoke.ChatStream(t.Context(), &invoke.ModelConfig{
		Provider: "gemini", ModelID: "m", ModelName: "gemini-2.5-flash", BaseURL: srv.URL,
		Credentials: invoke.Credentials{APIKey: "k"},
	}, &invoke.ChatOptions{Stream: true, Messages: []invoke.Message{invoke.TextMessage(invoke.RoleUser, "hi")}})
	require.NoError(t, err)

	var (
		text  strings.Builder
		usage *struct {
			prompt, completion, total int
		}
		finish string
		done   bool
		calls  int
	)
	for sr := range ch {
		if sr.ResponseType == "answer" && sr.Content != "" {
			text.WriteString(sr.Content)
		}
		if sr.Usage != nil {
			usage = &struct{ prompt, completion, total int }{
				sr.Usage.PromptTokens, sr.Usage.CompletionTokens, sr.Usage.TotalTokens,
			}
		}
		if len(sr.ToolCalls) > 0 {
			calls = len(sr.ToolCalls)
		}
		if sr.Done {
			done = true
			finish = sr.FinishReason
		}
	}
	assert.True(t, done)
	assert.Equal(t, "hello", text.String())
	assert.Equal(t, "tool_calls", finish, "the terminating frame carried a functionCall")
	assert.Equal(t, 1, calls, "the whole-call fragment rides Done.ToolCalls")
	require.NotNil(t, usage, "usage must ride the Done chunk (usage-in-Done seam)")
	assert.Equal(t, 4, usage.prompt)
	assert.Equal(t, 2, usage.completion)
	assert.Equal(t, 6, usage.total)
}

// P1-1: a non-terminating frame with MULTIPLE parts folds everything — the
// call enters the accumulator (Done.ToolCalls), the texts concatenate into
// one delta (openai-bridge fold semantics; nothing dropped).
func TestGeminiStreamMultiPartFrame(t *testing.T) {
	a := &GeminiAdapter{}
	state := invoke.NewStreamBridgeState()
	frame := `{"candidates":[{"content":{"parts":[
		{"text":"a"},{"functionCall":{"name":"f1","args":{}}},
		{"text":"b"},{"functionCall":{"name":"f2","args":{}}}]}}]}`
	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(frame)})
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, invoke.StreamKindAnswer, ev.Kind, "text wins the frame's single event slot")
	assert.Equal(t, "ab", ev.Delta.Text)

	// terminator frame: both accumulated calls ride Done.ToolCalls
	term := `{"candidates":[{"content":{"parts":[{"text":"c"}]},"finishReason":"STOP"}]}`
	ev, err = a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(term)})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	assert.Equal(t, "c", ev.Delta.Text, "terminator text rides Done.Delta")
	assert.Equal(t, "tool_calls", ev.Done.FinishReason)
	require.Len(t, ev.Done.ToolCalls, 2)
	assert.Equal(t, "f1", ev.Done.ToolCalls[0].Function.Name)
	assert.Equal(t, "f2", ev.Done.ToolCalls[1].Function.Name)
}

// A non-terminating call-only frame still streams the whole-call delta live.
func TestGeminiStreamCallOnlyFrame(t *testing.T) {
	a := &GeminiAdapter{}
	state := invoke.NewStreamBridgeState()
	frame := `{"candidates":[{"content":{"parts":[
		{"functionCall":{"name":"f","args":{"x":1}}},
		{"functionCall":{"name":"g","args":{"y":2}}}]}}]}`
	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(frame)})
	require.NoError(t, err)
	require.NotNil(t, ev.ToolCallDelta, "first call takes the frame's event slot")
	assert.Equal(t, "f", ev.ToolCallDelta.Name)
	assert.Equal(t, 0, ev.ToolCallDelta.Index)
}
