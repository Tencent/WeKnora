package openairesponses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

func newTestClient(t *testing.T, server *httptest.Server, reasoning bool) *Client {
	t.Helper()
	ep := api.Endpoint{
		Model:   "gpt-5",
		ModelID: "model-1",
		Auth:    api.BearerAuth("sk-test"),
	}
	if server != nil {
		ep.BaseURL = server.URL
		ep.Client = server.Client()
	} else {
		ep.BaseURL = "https://api.openai.com/v1"
	}
	return New(Config{
		Endpoint:  ep,
		Settings:  api.DefaultOpenAIResponses(),
		Reasoning: reasoning,
		SessionID: "session-fallback",
	})
}

// roundTrip marshals and re-decodes the body so struct-typed values compare
// as plain JSON.
func roundTrip(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return out
}

func conversation() []api.Message {
	return []api.Message{
		{Role: "system", Content: "You are a weather assistant."},
		{Role: "user", Content: "What's the weather in Paris?"},
		{
			Role: "assistant",
			ToolCalls: []api.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: api.FunctionCall{
					Name:      "get_weather",
					Arguments: `{"city":"Paris"}`,
				},
				ProviderMetadata: types.ToolCallMetadata{
					"openai_responses_item_id": json.RawMessage(`"fc_1"`),
				},
			}},
			ReasoningMetadata: types.ProviderMetadata{
				"openai_responses_reasoning": json.RawMessage(
					`[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc-abc"}]`),
			},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "get_weather", Content: "Sunny, 24C"},
	}
}

func TestBuildRequestBodyGolden(t *testing.T) {
	c := newTestClient(t, nil, true)
	opts := &api.Options{
		Temperature:         0.7,
		MaxCompletionTokens: 1000,
		ReasoningEffort:     api.ReasoningHigh,
		PromptCacheKey:      "cache-key-1",
		Tools: []api.Tool{{
			Type: "function",
			Function: api.FunctionDef{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			},
		}},
		ToolChoice: "auto",
	}
	body, err := c.BuildRequestBody(conversation(), opts, false)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	got := roundTrip(t, body)

	want := map[string]any{
		"model":        "gpt-5",
		"store":        false,
		"instructions": "You are a weather assistant.",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "What's the weather in Paris?"},
			map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": "enc-abc"},
			map[string]any{
				"type": "function_call", "id": "fc_1", "call_id": "call_1",
				"name": "get_weather", "arguments": `{"city":"Paris"}`,
			},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "Sunny, 24C"},
		},
		"max_output_tokens": float64(1000),
		"tools": []any{
			map[string]any{
				"type":        "function",
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]any{
					"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}},
				},
			},
		},
		"tool_choice":      "auto",
		"reasoning":        map[string]any{"effort": "high", "summary": "auto"},
		"include":          []any{"reasoning.encrypted_content"},
		"prompt_cache_key": "cache-key-1",
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("body mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
	for _, absent := range []string{"temperature", "stream", "messages", "frequency_penalty", "presence_penalty"} {
		if _, ok := got[absent]; ok {
			t.Errorf("unexpected key %q in body", absent)
		}
	}
}

func TestBuildRequestBodyNonReasoningStream(t *testing.T) {
	c := newTestClient(t, nil, false)
	c.cfg.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningOff: api.StringPtr("none")}
	off := false
	opts := &api.Options{
		Temperature:    0.3,
		TopP:           0.9,
		Thinking:       &off,
		CacheRetention: api.CacheRetentionLong,
		MaxTokens:      4,
		Format:         json.RawMessage(`{"type":"object"}`),
	}
	body, err := c.BuildRequestBody([]api.Message{{Role: "user", Content: "hi"}}, opts, true)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	got := roundTrip(t, body)

	if got["stream"] != true {
		t.Errorf("stream = %v, want true", got["stream"])
	}
	if got["temperature"] != 0.3 || got["top_p"] != 0.9 {
		t.Errorf("sampling = %v / %v", got["temperature"], got["top_p"])
	}
	if got["max_output_tokens"] != float64(16) {
		t.Errorf("max_output_tokens = %v, want 16 (minimum)", got["max_output_tokens"])
	}
	if got["prompt_cache_key"] != "session-fallback" {
		t.Errorf("prompt_cache_key = %v", got["prompt_cache_key"])
	}
	if got["prompt_cache_retention"] != "24h" {
		t.Errorf("prompt_cache_retention = %v", got["prompt_cache_retention"])
	}
	if _, ok := got["include"]; ok {
		t.Errorf("include must not be sent for non-reasoning models")
	}
	reasoning, _ := got["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Errorf("reasoning = %v, want effort none", got["reasoning"])
	}
	text, _ := got["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Errorf("text.format = %v", got["text"])
	}
	input, _ := got["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %v", got["input"])
	}
	msg := input[0].(map[string]any)
	if content, _ := msg["content"].(string); !strings.HasSuffix(content, `Use this JSON schema: {"type":"object"}`) {
		t.Errorf("schema hint not appended: %q", content)
	}
}

func TestBuildRequestBodyOffWithoutMapping(t *testing.T) {
	c := newTestClient(t, nil, false)
	off := false
	body, err := c.BuildRequestBody([]api.Message{{Role: "user", Content: "hi"}}, &api.Options{Thinking: &off}, false)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("reasoning must be omitted when off has no vendor value: %v", body["reasoning"])
	}
}

func TestBuildRequestBodyImagesAndAuto(t *testing.T) {
	c := newTestClient(t, nil, true)
	on := true
	msgs := []api.Message{{
		Role: "user",
		MultiContent: []api.MessageContentPart{
			{Type: "text", Text: "Describe"},
			{Type: "image_url", ImageURL: &api.ImageURL{URL: "https://example.com/a.png"}},
		},
	}}
	body, err := c.BuildRequestBody(msgs, &api.Options{Thinking: &on}, false)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	got := roundTrip(t, body)
	input := got["input"].([]any)
	parts := input[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts = %v", parts)
	}
	img := parts[1].(map[string]any)
	if img["type"] != "input_image" || img["image_url"] != "https://example.com/a.png" || img["detail"] != "auto" {
		t.Errorf("image part = %v", img)
	}
	reasoning := got["reasoning"].(map[string]any)
	if _, ok := reasoning["effort"]; ok {
		t.Errorf("auto must not send effort: %v", reasoning)
	}
	if reasoning["summary"] != "auto" {
		t.Errorf("reasoning summary = %v", reasoning)
	}
}

func TestChatDecodesResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var gotPath string
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "resp_1",
			"status": "completed",
			"output": [
				{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Think A"},
					{"type":"summary_text","text":"Think B"}],"encrypted_content":"enc"},
				{"type":"message","id":"msg_1","role":"assistant","status":"completed",
					"content":[{"type":"output_text","text":"Hello "},{"type":"output_text","text":"world"}]},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather",
					"arguments":"{\"city\":\"Paris\"}","status":"completed"}
			],
			"usage": {"input_tokens": 100, "output_tokens": 20, "total_tokens": 120,
				"input_tokens_details": {"cached_tokens": 60}}
		}`))
	}))
	defer server.Close()

	c := newTestClient(t, server, true)
	resp, err := c.Chat(
		context.Background(),
		[]api.Message{{Role: "user", Content: "hi"}},
		&api.Options{PromptCacheKey: "k1"},
	)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotPath != "/responses" {
		t.Errorf("path = %q", gotPath)
	}
	if gotHeaders.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotHeaders.Get("Authorization"))
	}
	if gotHeaders.Get("x-session-affinity") != "k1" {
		t.Errorf("affinity header = %q", gotHeaders.Get("x-session-affinity"))
	}
	if resp.Content != "Hello world" {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.ReasoningContent != "Think A\nThink B" {
		t.Errorf("reasoning = %q", resp.ReasoningContent)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish = %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "get_weather" ||
		tc.Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.ProviderMetadata["openai_responses_item_id"]) != `"fc_1"` {
		t.Errorf("item id metadata = %s", tc.ProviderMetadata["openai_responses_item_id"])
	}
	var items []map[string]any
	if err := json.Unmarshal(resp.ReasoningMetadata["openai_responses_reasoning"], &items); err != nil {
		t.Fatalf("reasoning metadata: %v", err)
	}
	if len(items) != 1 || items[0]["id"] != "rs_1" || items[0]["encrypted_content"] != "enc" {
		t.Errorf("reasoning items = %v", items)
	}
	u := resp.Usage
	if u.PromptTokens != 100 || u.CompletionTokens != 20 || u.TotalTokens != 120 {
		t.Errorf("usage = %+v", u)
	}
	if u.CacheReadTokens != 60 || u.CacheMissTokens != 40 || !u.CacheReported ||
		u.CacheStatus != types.PromptCacheStatusHit {
		t.Errorf("cache usage = %+v", u)
	}
}

func TestChatIncompleteAndError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	payload := `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":` +
		`[{"type":"message","content":[{"type":"output_text","text":"partial"}]}],` +
		`"usage":{"input_tokens":1,"output_tokens":2}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	c := newTestClient(t, server, false)
	resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.FinishReason != "length" || resp.Content != "partial" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.TotalTokens != 3 || resp.Usage.CacheReported ||
		resp.Usage.CacheStatus != types.PromptCacheStatusUnreported {
		t.Errorf("usage = %+v", resp.Usage)
	}

	payload = `{"error":{"type":"invalid_request_error","message":"bad model"}}`
	_, err = c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("expected API error, got %v", err)
	}
}

func sseBody(events ...string) string {
	var b strings.Builder
	for _, ev := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(ev), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, ev)
	}
	return b.String()
}

func TestChatStreamAssemblesToolCall(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	body := sseBody(
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta",`+
			`"item_id":"rs_1","output_index":0,"summary_index":0,"delta":"thinking..."}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning",`+
			`"id":"rs_1","summary":[{"type":"summary_text","text":"thinking..."}],"encrypted_content":"enc"}}`,
		`{"type":"response.output_item.added","output_index":1,`+
			`"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"delta":"Hello"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"delta":" world"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message",`+
			`"id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hello world"}]}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call",`+
			`"id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"\"Paris\"}"}`,
		`{"type":"response.function_call_arguments.done",`+
			`"item_id":"fc_1","output_index":2,"arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":`+
			`"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}","status":"completed"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_to`+
			`kens":50,"output_tokens":10,"total_tokens":60,"input_tokens_details":{"cached_tokens":0}}}}`,
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(raw, &req); err != nil || req["stream"] != true {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c := newTestClient(t, server, true)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var events []types.StreamResponse
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}

	var thinking, answer string
	var sawToolCallEvent, sawThinkingDone bool
	for _, ev := range events {
		switch ev.ResponseType {
		case types.ResponseTypeThinking:
			if ev.Done {
				sawThinkingDone = true
			} else {
				thinking += ev.Content
			}
		case types.ResponseTypeAnswer:
			answer += ev.Content
		case types.ResponseTypeToolCall:
			sawToolCallEvent = true
			if ev.Data["tool_name"] != "get_weather" || ev.Data["tool_call_id"] != "call_1" {
				t.Errorf("tool call event data = %v", ev.Data)
			}
		case types.ResponseTypeError:
			t.Fatalf("unexpected error event: %s", ev.Content)
		}
	}
	if thinking != "thinking..." || !sawThinkingDone {
		t.Errorf("thinking = %q done=%t", thinking, sawThinkingDone)
	}
	if answer != "Hello world" {
		t.Errorf("answer = %q", answer)
	}
	if !sawToolCallEvent {
		t.Error("missing ResponseTypeToolCall event")
	}

	final := events[len(events)-1]
	if final.ResponseType != types.ResponseTypeAnswer || !final.Done {
		t.Fatalf("final event = %+v", final)
	}
	if final.Usage == nil || final.Usage.PromptTokens != 50 || final.Usage.CompletionTokens != 10 ||
		final.Usage.TotalTokens != 60 || !final.Usage.CacheReported {
		t.Errorf("final usage = %+v", final.Usage)
	}
	if final.FinishReason != "tool_calls" {
		t.Errorf("final finish = %q", final.FinishReason)
	}
	if len(final.ToolCalls) != 1 {
		t.Fatalf("final tool calls = %+v", final.ToolCalls)
	}
	tc := final.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "get_weather" ||
		tc.Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("assembled tool call = %+v", tc)
	}
	if string(tc.ProviderMetadata["openai_responses_item_id"]) != `"fc_1"` {
		t.Errorf("item id metadata = %s", tc.ProviderMetadata["openai_responses_item_id"])
	}
	md, _ := final.Data["reasoning_metadata"].(types.ProviderMetadata)
	var items []map[string]any
	err = json.Unmarshal(md["openai_responses_reasoning"], &items)
	if err != nil || len(items) != 1 || items[0]["encrypted_content"] != "enc" {
		t.Errorf("reasoning metadata = %v (err %v)", final.Data, err)
	}
}

func TestChatStreamFailedEvent(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	body := sseBody(
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"boom"}}}`,
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c := newTestClient(t, server, false)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var last types.StreamResponse
	for ev := range ch {
		last = ev
	}
	if last.ResponseType != types.ResponseTypeError || !strings.Contains(last.Content, "boom") ||
		last.FinishReason != types.FinishReasonIncomplete {
		t.Fatalf("last event = %+v", last)
	}
}

func TestOffLevelMapsToNone(t *testing.T) {
	c := newTestClient(t, nil, true)
	c.cfg.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningOff: api.StringPtr("none")}
	body, err := c.BuildRequestBody(
		[]api.Message{{Role: "user", Content: "hi"}},
		&api.Options{ReasoningEffort: api.ReasoningOff},
		false,
	)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %v, want effort none", body["reasoning"])
	}
	if _, ok := reasoning["summary"]; ok {
		t.Errorf("summary must not accompany an off request: %v", reasoning)
	}
}

// Gateways that implement the Responses protocol without item ids exist.
// Correlating argument deltas by item_id alone routed every call's arguments
// to the single index keyed by "", merging two distinct tool calls into one
// and leaving the other with an empty argument string.
func TestChatStreamCorrelatesToolCallsWithoutItemIDs(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	body := sseBody(
		`{"type":"response.output_item.added","output_index":0,`+
			`"item":{"type":"function_call","call_id":"call_a","name":"alpha","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\":1}"}`,
		`{"type":"response.output_item.added","output_index":1,`+
			`"item":{"type":"function_call","call_id":"call_b","name":"beta","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"b\":2}"}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := newTestClient(t, server, true)
	ch, err := client.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var last types.StreamResponse
	for chunk := range ch {
		if chunk.ResponseType == types.ResponseTypeError {
			t.Fatalf("unexpected error chunk: %s", chunk.Content)
		}
		if chunk.Done && chunk.ResponseType == types.ResponseTypeAnswer {
			last = chunk
		}
	}
	if len(last.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d (%#v), want 2", len(last.ToolCalls), last.ToolCalls)
	}
	want := []struct{ id, name, args string }{
		{"call_a", "alpha", `{"a":1}`},
		{"call_b", "beta", `{"b":2}`},
	}
	for i, w := range want {
		got := last.ToolCalls[i]
		if got.ID != w.id || got.Function.Name != w.name || got.Function.Arguments != w.args {
			t.Fatalf("tool call %d = %+v, want id=%s name=%s args=%s", i, got, w.id, w.name, w.args)
		}
	}
}

// Responses is an event-typed protocol, so an event whose *type* we do not
// know is routine and the switch ignores it. A payload that is not JSON at
// all is a different animal: a truncated frame or a gateway error page.
// Skipping it ran the loop to EOF, which reached the caller as a complete
// answer and got stored as the model's reply.
func TestChatStreamUndecodableEventFailsTheStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	body := sseBody(
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"Hel"}`,
		// An unknown but well-formed event type stays harmless.
		`{"type":"response.web_search_call.in_progress","item_id":"ws_1","output_index":1}`,
	) + "event: response.output_text.delta\ndata: <html>502 Bad Gateway</html>\n\n" +
		sseBody(
			`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"lo"}`,
			`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
		)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c := newTestClient(t, server, false)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var answer string
	var sawError bool
	for ev := range ch {
		if ev.ResponseType == types.ResponseTypeError {
			sawError = true
			if !strings.Contains(ev.Content, "decode stream chunk") {
				t.Errorf("error content = %q", ev.Content)
			}
			continue
		}
		answer += ev.Content
	}
	if !sawError {
		t.Error("a truncated stream must surface as an error, not as a short answer")
	}
	if answer != "Hel" {
		t.Errorf("answer = %q, decoding must stop at the bad frame", answer)
	}
}

// An output item that will not decode may be the round's function_call.
// Dropping it hands the agent a plain answer with nothing to act on.
func TestChatMalformedOutputItemIsAnError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output":[` +
			`{"type":"message","id":"msg_1","role":"assistant",` +
			`"content":[{"type":"output_text","text":"done"}]},` +
			`{"type":"function_call","id":"fc_1","call_id":"call_1","name":{"broken":true}}]}`))
	}))
	defer server.Close()

	c := newTestClient(t, server, false)
	_, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "decode output item") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

// The streaming counterpart: an item event we cannot read fails the round
// instead of letting the tool call disappear from it.
func TestChatStreamMalformedOutputItemFailsTheStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	body := sseBody(
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"delta":"ok"}`,
		`{"type":"response.output_item.added","output_index":1,`+
			`"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":{"broken":true}}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c := newTestClient(t, server, false)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var last types.StreamResponse
	var answer string
	for ev := range ch {
		if ev.ResponseType != types.ResponseTypeError {
			answer += ev.Content
		}
		last = ev
	}
	if last.ResponseType != types.ResponseTypeError || !strings.Contains(last.Content, "decode output item") {
		t.Fatalf("last event = %+v", last)
	}
	if answer != "ok" {
		t.Errorf("answer = %q, decoding must stop at the bad item", answer)
	}
}
