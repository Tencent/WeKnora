package googlegenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

func newTestClient(t *testing.T, baseURL string, mutate func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Endpoint: api.Endpoint{
			BaseURL: baseURL,
			Model:   "gemini-2.5-pro",
			ModelID: "model-123",
			Auth:    api.HeaderAuth("x-goog-api-key", "test-key"),
		},
		Settings:  api.DefaultGoogleGenerativeAI(),
		Reasoning: true,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg)
}

// normalize round-trips a body through JSON so typed slices and ints compare
// equal to a parsed expectation.
func normalize(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func parseJSON(t *testing.T, s string) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("parse expected json: %v", err)
	}
	return out
}

func goldenMessages() []api.Message {
	return []api.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", MultiContent: []api.MessageContentPart{
			{Type: "image_url", ImageURL: &api.ImageURL{URL: "data:image/png;base64,AAAA"}},
			{Type: "text", Text: "What is this?"},
		}},
		{Role: "assistant", ReasoningContent: "never replayed", ToolCalls: []api.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: api.FunctionCall{
					Name:      "get_weather",
					Arguments: `{"city":"Paris"}`,
				},
				ProviderMetadata: types.ToolCallMetadata{"thought_signature": json.RawMessage(`"sig-1"`)},
			},
			{ID: "call_2", Type: "function", Function: api.FunctionCall{Name: "get_time", Arguments: ""}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: `{"temp": 20}`},
		{Role: "tool", ToolCallID: "call_2", Name: "get_time", Content: "noon"},
	}
}

func goldenOptions() *api.Options {
	return &api.Options{
		Temperature:     0.7,
		MaxTokens:       1024,
		ReasoningEffort: api.ReasoningHigh,
		ToolChoice:      "auto",
		Tools: []api.Tool{
			{Type: "function", Function: api.FunctionDef{
				Name:        "get_weather",
				Description: "Weather",
				Parameters: json.RawMessage(`{
					"$schema": "http://json-schema.org/draft-07/schema#",
					"type": "object",
					"title": "Weather",
					"additionalProperties": false,
					"properties": {
						"city": {"type": "string", "default": "Paris", "examples": ["Paris"]},
						"title": {"type": "string", "description": "a property named title"}
					},
					"required": ["city"]
				}`),
			}},
			{Type: "function", Function: api.FunctionDef{Name: "get_time", Description: "Time"}},
		},
	}
}

const goldenBase = `{
  "systemInstruction": {"parts": [{"text": "You are helpful."}]},
  "contents": [
    {"role": "user", "parts": [
      {"inlineData": {"mimeType": "image/png", "data": "AAAA"}},
      {"text": "What is this?"}
    ]},
    {"role": "model", "parts": [
      {"functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "Paris"}}, "thoughtSignature": "sig-1"},
      {"functionCall": {"id": "call_2", "name": "get_time", "args": {}}}
    ]},
    {"role": "user", "parts": [
      {"functionResponse": {"id": "call_1", "name": "get_weather", "response": {"result": {"temp": 20}}}},
      {"functionResponse": {"id": "call_2", "name": "get_time", "response": {"result": "noon"}}}
    ]}
  ],
  "tools": [{"functionDeclarations": [
    {"name": "get_weather", "description": "Weather", "parameters": {
      "type": "object",
      "properties": {
        "city": {"type": "string"},
        "title": {"type": "string", "description": "a property named title"}
      },
      "required": ["city"]
    }},
    {"name": "get_time", "description": "Time"}
  ]}],
  "toolConfig": {"functionCallingConfig": {"mode": "AUTO"}},
  "generationConfig": {
    "temperature": 0.7,
    "maxOutputTokens": 1024,
    "thinkingConfig": THINKING
  }
}`

// TestThinkingConfigOmittedForNonReasoningModels guards models the catalog
// does not mark as reasoning (gemini-2.0-flash, Gemma): they may reject any
// thinkingConfig, including the thinkingBudget 0 an agent's default "off" maps to.
func TestThinkingConfigOmittedForNonReasoningModels(t *testing.T) {
	client := newTestClient(t, "https://generativelanguage.googleapis.com",
		func(c *Config) { c.Reasoning = false })
	off := false
	body, err := client.BuildRequestBody(
		[]api.Message{{Role: "user", Content: "hi"}}, &api.Options{Thinking: &off}, false)
	if err != nil {
		t.Fatal(err)
	}
	if gc, ok := body["generationConfig"].(map[string]any); ok {
		if _, has := gc["thinkingConfig"]; has {
			t.Fatalf("thinkingConfig sent for a non-reasoning model: %v", gc)
		}
	}
}

// TestForeignSignatureNotReplayed guards cross-provider replay: a signature
// issued by another protocol must never reach Gemini as thoughtSignature.
func TestForeignSignatureNotReplayed(t *testing.T) {
	client := newTestClient(t, "https://generativelanguage.googleapis.com", nil)
	body, err := client.BuildRequestBody([]api.Message{
		{Role: "user", Content: "hi"},
		{
			Role: "assistant", Content: "ok",
			ReasoningSignature: api.TagSignature(api.APIAnthropicMessages, "claude-sig"),
		},
		{Role: "user", Content: "again"},
	}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(body)
	if strings.Contains(string(data), "claude-sig") {
		t.Fatalf("foreign signature replayed: %s", data)
	}
}

func TestBuildRequestBodyGolden(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*Config)
		thinking string
	}{
		{
			name:     "budget mode",
			thinking: `{"thinkingBudget": 24576, "includeThoughts": true}`,
		},
		{
			name: "level mode",
			mutate: func(c *Config) {
				c.Settings.ThinkingMode = api.GoogleThinkingLevel
				c.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningHigh: api.StringPtr("high")}
			},
			thinking: `{"thinkingLevel": "high", "includeThoughts": true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, "https://generativelanguage.googleapis.com", tc.mutate)
			body, err := client.BuildRequestBody(goldenMessages(), goldenOptions(), false)
			if err != nil {
				t.Fatalf("BuildRequestBody: %v", err)
			}
			expected := parseJSON(t, replaceThinking(goldenBase, tc.thinking))
			got := normalize(t, body)
			if !reflect.DeepEqual(got, expected) {
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				wantJSON, _ := json.MarshalIndent(expected, "", "  ")
				t.Fatalf("body mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func replaceThinking(tmpl, thinking string) string {
	return strings.Replace(tmpl, "THINKING", thinking, 1)
}

func TestThinkingConfigVariants(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		opts   *api.Options
		want   string // "" means no thinkingConfig
	}{
		{name: "not requested", opts: &api.Options{}, want: ""},
		{
			name: "budget off supported", opts: &api.Options{ReasoningEffort: api.ReasoningOff},
			want: `{"thinkingBudget": 0}`,
		},
		{
			name:   "budget off unsupported",
			mutate: func(c *Config) { c.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningOff: nil} },
			opts:   &api.Options{ReasoningEffort: api.ReasoningOff},
			want:   "",
		},
		{
			name: "budget auto", opts: &api.Options{ReasoningEffort: api.ReasoningAuto},
			want: `{"thinkingBudget": -1, "includeThoughts": true}`,
		},
		{
			name: "budget explicit tokens",
			opts: &api.Options{ReasoningEffort: api.ReasoningLow, ThinkingBudgetTokens: 512},
			want: `{"thinkingBudget": 512, "includeThoughts": true}`,
		},
		{
			name:   "mode none",
			mutate: func(c *Config) { c.Settings.ThinkingMode = api.GoogleThinkingNone },
			opts:   &api.Options{ReasoningEffort: api.ReasoningHigh},
			want:   "",
		},
		{
			name:   "level off unmapped",
			mutate: func(c *Config) { c.Settings.ThinkingMode = api.GoogleThinkingLevel },
			opts:   &api.Options{ReasoningEffort: api.ReasoningOff},
			want:   "",
		},
		{
			name:   "level auto",
			mutate: func(c *Config) { c.Settings.ThinkingMode = api.GoogleThinkingLevel },
			opts:   &api.Options{ReasoningEffort: api.ReasoningAuto},
			want:   `{"includeThoughts": true}`,
		},
		{
			name: "level clamps unsupported rung",
			mutate: func(c *Config) {
				c.Settings.ThinkingMode = api.GoogleThinkingLevel
				c.ThinkingLevels = api.ThinkingLevelMap{
					api.ReasoningMedium: nil,
					api.ReasoningHigh:   api.StringPtr("high"),
				}
			},
			opts: &api.Options{ReasoningEffort: api.ReasoningMedium},
			want: `{"thinkingLevel": "high", "includeThoughts": true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, "https://generativelanguage.googleapis.com", tc.mutate)
			body, err := client.BuildRequestBody([]api.Message{{Role: "user", Content: "hi"}}, tc.opts, false)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if gen, ok := body["generationConfig"].(map[string]any); ok {
				if tcfg, ok := gen["thinkingConfig"]; ok {
					got = normalize(t, tcfg)
				}
			}
			var want any
			if tc.want != "" {
				want = parseJSON(t, tc.want)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("thinkingConfig = %v, want %v", got, want)
			}
		})
	}
}

func TestToolChoiceAndFormat(t *testing.T) {
	client := newTestClient(t, "https://generativelanguage.googleapis.com", nil)
	opts := &api.Options{
		ToolChoice: "get_weather",
		Tools:      []api.Tool{{Function: api.FunctionDef{Name: "get_weather"}}},
		Format:     json.RawMessage(`{"type":"object"}`),
	}
	body, err := client.BuildRequestBody([]api.Message{{Role: "user", Content: "hi"}}, opts, false)
	if err != nil {
		t.Fatal(err)
	}
	got := normalize(t, body["toolConfig"])
	want := parseJSON(t, `{"functionCallingConfig": {"mode": "ANY", "allowedFunctionNames": ["get_weather"]}}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toolConfig = %v, want %v", got, want)
	}
	gen := body["generationConfig"].(map[string]any)
	if gen["responseMimeType"] != "application/json" {
		t.Fatalf("responseMimeType = %v", gen["responseMimeType"])
	}
	contents := body["contents"].([]map[string]any)
	text := contents[0]["parts"].([]part)[0]["text"].(string)
	if text != "hi\nUse this JSON schema: {\"type\":\"object\"}" {
		t.Fatalf("schema hint not appended: %q", text)
	}
}

func TestRequestURL(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		stream bool
		want   string
	}{
		{
			"bare host non-stream", "https://generativelanguage.googleapis.com", false,
			"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent",
		},
		{
			"bare host trailing slash stream", "https://generativelanguage.googleapis.com/", true,
			"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
		},
		{
			"versioned base non-stream", "https://proxy.example.com/gemini/v1beta", false,
			"https://proxy.example.com/gemini/v1beta/models/gemini-2.5-pro:generateContent",
		},
		{
			"versioned base stream", "https://proxy.example.com/v1beta/", true,
			"https://proxy.example.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, tc.base, nil)
			if got := client.requestURL(tc.stream); got != tc.want {
				t.Fatalf("requestURL = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("explicit URL override", func(t *testing.T) {
		client := newTestClient(t, "https://ignored.example.com", func(c *Config) {
			c.Endpoint.URL = "https://custom.example.com/route:generateContent"
		})
		if got := client.requestURL(false); got != "https://custom.example.com/route:generateContent" {
			t.Fatalf("requestURL = %q", got)
		}
		// A streaming call must name the streaming method: ":generateContent"
		// answers with one buffered JSON document even with alt=sse, which the
		// SSE reader would see as an empty stream.
		want := "https://custom.example.com/route:streamGenerateContent?alt=sse"
		if got := client.requestURL(true); got != want {
			t.Fatalf("requestURL = %q, want %q", got, want)
		}
	})

	t.Run("explicit URL override without a known method is left alone", func(t *testing.T) {
		client := newTestClient(t, "https://ignored.example.com", func(c *Config) {
			c.Endpoint.URL = "https://custom.example.com/route"
		})
		if got := client.requestURL(true); got != "https://custom.example.com/route?alt=sse" {
			t.Fatalf("requestURL = %q", got)
		}
	})
}

func TestChatDecodesResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var gotPath, gotQuery, gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotKey = r.Header.Get("x-goog-api-key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "candidates": [{
		    "content": {"role": "model", "parts": [
		      {"text": "Thinking about Paris.", "thought": true, "thoughtSignature": "sig-thought"},
		      {"text": "Let me check."},
		      {"functionCall": {"id": "fc-1", "name": "get_weather", "args": {"city": "Paris"}},
		       "thoughtSignature": "sig-fc"}
		    ]},
		    "finishReason": "STOP"
		  }],
		  "usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "thoughtsTokenCount": 3,
		    "totalTokenCount": 18, "cachedContentTokenCount": 4}
		}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	resp, err := client.Chat(context.Background(), []api.Message{{Role: "user", Content: "weather?"}}, &api.Options{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotPath != "/v1beta/models/gemini-2.5-pro:generateContent" || gotQuery != "" {
		t.Fatalf("request path = %q query = %q", gotPath, gotQuery)
	}
	if gotKey != "test-key" {
		t.Fatalf("x-goog-api-key = %q", gotKey)
	}
	if _, ok := gotBody["contents"]; !ok {
		t.Fatalf("request body missing contents: %v", gotBody)
	}
	if resp.Content != "Let me check." {
		t.Fatalf("Content = %q", resp.Content)
	}
	if resp.ReasoningContent != "Thinking about Paris." {
		t.Fatalf("ReasoningContent = %q", resp.ReasoningContent)
	}
	if resp.ReasoningSignature != "google-generative-ai:sig-fc" {
		t.Fatalf("ReasoningSignature = %q", resp.ReasoningSignature)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "fc-1" || tc.Type != "function" || tc.Function.Name != "get_weather" ||
		tc.Function.Arguments != `{"city": "Paris"}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if string(tc.ProviderMetadata["thought_signature"]) != `"sig-fc"` {
		t.Fatalf("thought_signature = %s", tc.ProviderMetadata["thought_signature"])
	}
	u := resp.Usage
	if u.PromptTokens != 10 || u.CompletionTokens != 8 || u.TotalTokens != 18 {
		t.Fatalf("usage = %+v", u)
	}
	if u.CacheReadTokens != 4 || u.CacheMissTokens != 6 || !u.CacheReported ||
		u.CacheStatus != types.PromptCacheStatusHit {
		t.Fatalf("cache usage = %+v", u)
	}
}

func TestChatErrors(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"error payload", `{"error": {"code": 400, "message": "bad request", "status": "INVALID_ARGUMENT"}}`,
			"API error: bad request",
		},
		{"blocked prompt", `{"promptFeedback": {"blockReason": "SAFETY"}}`, "blocked: SAFETY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			client := newTestClient(t, srv.URL, nil)
			_, err := client.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestChatStreamDecodesSSE(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me think.","thought":true}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"Checking "}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"weather."}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather",` +
				`"args":{"city":"Paris"}},"thoughtSignature":"sig-stream"}]},"finishReason":"STOP"}],` +
				`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":3,` +
				`"totalTokenCount":18}}`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	ch, err := client.ChatStream(context.Background(),
		[]api.Message{{Role: "user", Content: "weather?"}}, &api.Options{})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var events []types.StreamResponse
	for ev := range ch {
		events = append(events, ev)
	}
	if gotPath != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" || gotQuery != "alt=sse" {
		t.Fatalf("request path = %q query = %q", gotPath, gotQuery)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}

	firstThinking, firstAnswer, toolCallEvent := -1, -1, -1
	var thinking, answer string
	for i, ev := range events {
		switch ev.ResponseType {
		case types.ResponseTypeThinking:
			if !ev.Done {
				if firstThinking < 0 {
					firstThinking = i
				}
				thinking += ev.Content
			}
		case types.ResponseTypeAnswer:
			if ev.Content != "" {
				if firstAnswer < 0 {
					firstAnswer = i
				}
				answer += ev.Content
			}
		case types.ResponseTypeToolCall:
			if toolCallEvent < 0 {
				toolCallEvent = i
			}
			if ev.Data["tool_name"] != "get_weather" || ev.Data["tool_call_id"] == "" {
				t.Fatalf("tool_call event data = %v", ev.Data)
			}
		case types.ResponseTypeError:
			t.Fatalf("stream error: %s", ev.Content)
		}
	}
	if firstThinking < 0 || firstAnswer < 0 || firstThinking > firstAnswer {
		t.Fatalf("thinking (%d) must precede answer (%d)", firstThinking, firstAnswer)
	}
	if thinking != "Let me think." || answer != "Checking weather." {
		t.Fatalf("thinking = %q answer = %q", thinking, answer)
	}
	if toolCallEvent < 0 {
		t.Fatal("no ResponseTypeToolCall event")
	}

	final := events[len(events)-1]
	if final.ResponseType != types.ResponseTypeAnswer || !final.Done {
		t.Fatalf("final event = %+v", final)
	}
	if final.FinishReason != "tool_calls" {
		t.Fatalf("final FinishReason = %q", final.FinishReason)
	}
	if final.Usage == nil || final.Usage.PromptTokens != 10 || final.Usage.CompletionTokens != 8 ||
		final.Usage.TotalTokens != 18 {
		t.Fatalf("final usage = %+v", final.Usage)
	}
	if final.Usage.CacheStatus != types.PromptCacheStatusUnreported || final.Usage.CacheReported {
		t.Fatalf("cache status = %+v", final.Usage)
	}
	if len(final.ToolCalls) != 1 {
		t.Fatalf("final tool calls = %+v", final.ToolCalls)
	}
	tc := final.ToolCalls[0]
	if tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"city":"Paris"}` || tc.Type != "function" {
		t.Fatalf("tool call = %+v", tc)
	}
	if len(tc.ID) != len("call_")+12 || tc.ID[:5] != "call_" {
		t.Fatalf("generated tool call id = %q", tc.ID)
	}
	if string(tc.ProviderMetadata["thought_signature"]) != `"sig-stream"` {
		t.Fatalf("thought_signature = %s", tc.ProviderMetadata["thought_signature"])
	}
	if final.Data["reasoning_signature"] != "google-generative-ai:sig-stream" {
		t.Fatalf("Data = %v", final.Data)
	}
}

func TestChatStreamErrorPayload(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: {\"error\": {\"code\": 429, \"message\": \"quota exceeded\"}}\n\n"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	ch, err := client.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var last types.StreamResponse
	for ev := range ch {
		last = ev
	}
	if last.ResponseType != types.ResponseTypeError || last.Content != "API stream error: quota exceeded" {
		t.Fatalf("last event = %+v", last)
	}
}

// "contents" is required and must be an array. A request that produced no
// turns (only system prompts) used to marshal a nil slice as null, which
// generateContent rejects with 400.
func TestBuildRequestBodyContentsNeverNull(t *testing.T) {
	client := newTestClient(t, "https://generativelanguage.googleapis.com", nil)
	for _, msgs := range [][]api.Message{nil, {{Role: "system", Content: "be brief"}}} {
		body, err := client.BuildRequestBody(msgs, nil, false)
		if err != nil {
			t.Fatalf("BuildRequestBody: %v", err)
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(data), `"contents":null`) {
			t.Fatalf("contents marshalled as null: %s", data)
		}
		if !strings.Contains(string(data), `"contents":[]`) {
			t.Fatalf("contents not an empty array: %s", data)
		}
	}
}

// Without usageMetadata the cache status must still be classified, otherwise
// "the vendor reported nothing" is indistinguishable from a real miss on the
// usage dashboards.
func TestChatClassifiesCacheStatusWithoutUsageMetadata(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},` +
			`"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	resp, err := client.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.CacheStatus == "" {
		t.Fatal("CacheStatus is empty; the usage block must still be classified")
	}
}

// A chunk that blocks the prompt mid-stream must surface as an error rather
// than being skipped as "no candidates".
func TestChatStreamSurfacesPromptFeedbackBlock(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"}}\n\n"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, nil)
	ch, err := client.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var sawError bool
	for chunk := range ch {
		if chunk.ResponseType == types.ResponseTypeError {
			sawError = true
			if !strings.Contains(chunk.Content, "SAFETY") {
				t.Fatalf("error chunk = %q, want the block reason", chunk.Content)
			}
		}
	}
	if !sawError {
		t.Fatal("blocked prompt did not produce an error chunk")
	}
}
