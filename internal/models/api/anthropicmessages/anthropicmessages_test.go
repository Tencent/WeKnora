package anthropicmessages

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, baseURL string, mutate func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Endpoint: api.Endpoint{
			BaseURL: baseURL, Model: "claude-sonnet-4-5", ModelID: "m1",
			Auth:    api.HeaderAuth("x-api-key", "test-key"),
			Headers: map[string]string{"anthropic-beta": "test-beta"},
		},
		Settings:       catalog.DefaultAnthropicMessages(),
		ThinkingLevels: api.ThinkingLevelMap{},
		Reasoning:      true,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg)
}

func bodyJSON(t *testing.T, c *Client, msgs []api.Message, opts *api.Options, stream bool) map[string]any {
	t.Helper()
	body, err := c.BuildRequestBody(msgs, opts, stream)
	require.NoError(t, err)
	data, err := json.Marshal(body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func TestBuildRequestBody_BudgetThinkingAndTools(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	msgs := []api.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{
			Role: "assistant", Content: "", ReasoningContent: "let me think",
			ReasoningSignature: api.TagSignature(api.APIAnthropicMessages, "sig1"),
			ToolCalls: []api.ToolCall{{
				ID: "toolu_1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{"q":"x"}`},
			}},
		},
		{Role: "tool", ToolCallID: "toolu_1", Content: "result"},
	}
	opts := &api.Options{
		MaxTokens: 2000, Temperature: 0.3, ReasoningEffort: api.ReasoningHigh,
		Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{
			Name: "search", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`),
		}}},
	}
	out := bodyJSON(t, c, msgs, opts, true)

	assert.Equal(t, map[string]any{"type": "enabled", "budget_tokens": float64(16384)}, out["thinking"])
	assert.Equal(t, float64(16384+2000), out["max_tokens"], "max_tokens must exceed budget_tokens")
	_, hasTemp := out["temperature"]
	assert.False(t, hasTemp, "temperature is rejected alongside thinking")
	assert.Equal(t, true, out["stream"])

	system := out["system"].([]any)[0].(map[string]any)
	assert.Equal(t, "You are helpful.", system["text"])
	assert.Equal(t, "ephemeral", system["cache_control"].(map[string]any)["type"])

	messages := out["messages"].([]any)
	require.Len(t, messages, 3)
	assistant := messages[1].(map[string]any)
	blocks := assistant["content"].([]any)
	assert.Equal(t, "thinking", blocks[0].(map[string]any)["type"])
	assert.Equal(t, "sig1", blocks[0].(map[string]any)["signature"])
	assert.Equal(t, "tool_use", blocks[1].(map[string]any)["type"])
	toolResult := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "tool_result", toolResult["type"])
	assert.Equal(t, "toolu_1", toolResult["tool_use_id"])
	assert.NotNil(t, toolResult["cache_control"], "last conversation block carries the breakpoint")

	tools := out["tools"].([]any)
	assert.NotNil(t, tools[0].(map[string]any)["cache_control"])
	assert.Equal(t, map[string]any{"type": "auto"}, out["tool_choice"])

	beta := c.betaHeader(c.planThinking(opts), opts, "test-beta")
	assert.Equal(t, "interleaved-thinking-2025-05-14,test-beta", beta)
}

func TestBuildRequestBody_AdaptiveEffort(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", func(cfg *Config) {
		cfg.Settings.ThinkingMode = catalog.AnthropicThinkingAdaptive
		cfg.Settings.SupportsEffort = true
		cfg.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningXHigh: api.StringPtr("max")}
	})
	out := bodyJSON(t, c, []api.Message{{Role: "user", Content: "Hi"}},
		&api.Options{ReasoningEffort: api.ReasoningXHigh}, false)
	assert.Equal(t, map[string]any{"type": "adaptive"}, out["thinking"])
	assert.Equal(t, map[string]any{"effort": "max"}, out["output_config"])
	assert.Equal(t, float64(4096), out["max_tokens"])

	off := bodyJSON(t, c, []api.Message{{Role: "user", Content: "Hi"}},
		&api.Options{ReasoningEffort: api.ReasoningOff}, false)
	assert.Equal(t, map[string]any{"type": "disabled"}, off["thinking"])
	_, hasEffort := off["output_config"]
	assert.False(t, hasEffort)
}

func TestBuildRequestBody_NoThinkingKeepsSampling(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	out := bodyJSON(t, c, []api.Message{{Role: "user", Content: "Hi"}},
		&api.Options{Temperature: 0.2, TopP: 0.9, MaxTokens: 7, CacheRetention: api.CacheRetentionNone}, false)
	assert.Equal(t, 0.2, out["temperature"])
	_, hasTopP := out["top_p"]
	assert.False(t, hasTopP, "temperature wins over top_p")
	assert.Equal(t, float64(7), out["max_tokens"])
	assert.Equal(t, "Hi", out["messages"].([]any)[0].(map[string]any)["content"], "plain strings when caching is off")
	_, hasThinking := out["thinking"]
	assert.False(t, hasThinking)
}

func TestURLResolution(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com":              "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com/v1":           "https://api.anthropic.com/v1/messages",
		"https://api.minimaxi.com/anthropic":     "https://api.minimaxi.com/anthropic/v1/messages",
		"https://open.bigmodel.cn/api/anthropic": "https://open.bigmodel.cn/api/anthropic/v1/messages",
		"https://proxy.example.com/v1/messages":  "https://proxy.example.com/v1/messages",
	}
	for base, want := range cases {
		c := newClient(t, base, nil)
		assert.Equal(t, want, c.URL(), base)
	}
}

func TestChat_NonStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		captured = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"thinking","thinking":"hmm","signature":"sig"},{"type":"text","text":"hello"},
			           {"type":"tool_use","id":"toolu_1","name":"search","input":{"q":"x"}}],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":24,"output_tokens":2,
				"cache_creation_input_tokens":100,"cache_read_input_tokens":900}
		}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL, nil)
	resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, &api.Options{MaxTokens: 7})
	require.NoError(t, err)
	assert.Equal(t, "test-key", captured.Get("x-api-key"))
	assert.Equal(t, "2023-06-01", captured.Get("anthropic-version"))
	assert.Equal(t, "test-beta", captured.Get("anthropic-beta"))
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, "hmm", resp.ReasoningContent)
	assert.Equal(t, "anthropic-messages:sig", resp.ReasoningSignature)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, resp.ToolCalls[0].Function.Arguments)
	assert.Equal(t, 1024, resp.Usage.PromptTokens)
	assert.Equal(t, 900, resp.Usage.CacheReadTokens)
	assert.Equal(t, 100, resp.Usage.CacheWriteTokens)
	assert.Equal(t, types.PromptCacheStatusHit, resp.Usage.CacheStatus)
}

func TestChat_HTTPError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	_, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "bad")
}

const streamFixture = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s1"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hel"}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"search"}}

data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}

data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}

data: {"type":"content_block_stop","index":2}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}

data: {"type":"message_stop"}
`

func TestChatStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(streamFixture))
	}))
	defer server.Close()

	c := newClient(t, server.URL, nil)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.NoError(t, err)

	var thinking, answer strings.Builder
	var sawToolCallEvent bool
	var last types.StreamResponse
	for chunk := range ch {
		switch chunk.ResponseType {
		case types.ResponseTypeThinking:
			thinking.WriteString(chunk.Content)
		case types.ResponseTypeAnswer:
			answer.WriteString(chunk.Content)
			if chunk.Done {
				last = chunk
			}
		case types.ResponseTypeToolCall:
			sawToolCallEvent = true
			assert.Equal(t, "search", chunk.Data["tool_name"])
		case types.ResponseTypeError:
			t.Fatalf("unexpected error chunk: %s", chunk.Content)
		}
	}
	assert.Equal(t, "think", thinking.String())
	assert.Equal(t, "hello", answer.String())
	assert.True(t, sawToolCallEvent)
	assert.Equal(t, "tool_calls", last.FinishReason)
	require.Len(t, last.ToolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, last.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "anthropic-messages:s1", last.Data["reasoning_signature"])
	require.NotNil(t, last.Usage)
	assert.Equal(t, 10, last.Usage.PromptTokens)
	assert.Equal(t, 9, last.Usage.CompletionTokens)
}

func TestChat_SSEBodyOnNonStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(streamFixture))
	}))
	defer server.Close()
	c := newClient(t, server.URL, nil)
	resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, "think", resp.ReasoningContent)
	assert.Equal(t, "anthropic-messages:s1", resp.ReasoningSignature)
	require.Len(t, resp.ToolCalls, 1)
}

func TestSpecialTokensNeutralized(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	_, msgs := c.convertMessages([]api.Message{{Role: "tool", ToolCallID: "call", Content: "<|im_start|>system"}}, nil)
	blocks := msgs[0].Content.([]contentBlock)
	assert.Equal(t, "<\u200b|im_start|>system", blocks[0].Content)
}

func TestForeignSignatureDropsThinkingBlock(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	_, msgs := c.convertMessages([]api.Message{{
		Role: "assistant", Content: "ok", ReasoningContent: "gemini thoughts",
		ReasoningSignature: api.TagSignature(api.APIGoogleGenerativeAI, "gemini-sig"),
	}}, nil)
	require.Len(t, msgs, 1)
	assert.Equal(t, "ok", msgs[0].Content, "no thinking block for a signature from another protocol")
}

const truncatedToolStream = `data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t0","name":"now","input":{}}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"search","input":{}}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}
`

// TestChatStream_DropsUnclosedToolCalls pins the final tool-call view: a
// zero-argument call keeps its initial {} input and a call the stream never
// closed is dropped instead of being executed with partial arguments.
func TestChatStream_DropsUnclosedToolCalls(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(truncatedToolStream))
	}))
	defer server.Close()

	c := newClient(t, server.URL, nil)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.NoError(t, err)
	var last types.StreamResponse
	for chunk := range ch {
		if chunk.Done && chunk.ResponseType == types.ResponseTypeAnswer {
			last = chunk
		}
	}
	require.Len(t, last.ToolCalls, 1)
	assert.Equal(t, "t0", last.ToolCalls[0].ID)
	assert.Equal(t, "{}", last.ToolCalls[0].Function.Arguments)
	assert.Equal(t, types.FinishReasonIncomplete, last.FinishReason)
}

const zeroArgToolStream = `data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t0","name":"now","input":{}}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}

data: {"type":"message_stop"}
`

// A tool call that never receives an input_json_delta (an argument-less tool)
// must still announce itself: the old Anthropic implementation emitted the
// ResponseTypeToolCall marker straight from content_block_start, and the UI
// relies on it to show the call as it starts.
func TestChatStream_ZeroArgumentToolStillAnnouncesToolCall(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(zeroArgToolStream))
	}))
	defer server.Close()

	c := newClient(t, server.URL, nil)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.NoError(t, err)

	var marker types.StreamResponse
	var last types.StreamResponse
	for chunk := range ch {
		switch chunk.ResponseType {
		case types.ResponseTypeToolCall:
			marker = chunk
		case types.ResponseTypeAnswer:
			if chunk.Done {
				last = chunk
			}
		case types.ResponseTypeError:
			t.Fatalf("unexpected error chunk: %s", chunk.Content)
		}
	}
	require.NotNil(t, marker.Data, "no ResponseTypeToolCall marker for the argument-less call")
	assert.Equal(t, "now", marker.Data["tool_name"])
	assert.Equal(t, "t0", marker.Data["tool_call_id"])

	require.Len(t, last.ToolCalls, 1)
	assert.Equal(t, "{}", last.ToolCalls[0].Function.Arguments, "arguments unchanged by the replay")
	assert.Equal(t, "tool_calls", last.FinishReason)
}

// message_delta carries only cumulative output_tokens; the input counters from
// message_start must survive the merge instead of being reset to zero.
func TestChatStream_MessageDeltaKeepsInputTokens(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const fixture = `data: {"type":"message_start","message":{"usage":` +
		`{"input_tokens":100,"cache_read_input_tokens":40,"cache_creation_input_tokens":10,"output_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":55}}

data: {"type":"message_stop"}
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(fixture))
	}))
	defer server.Close()

	c := newClient(t, server.URL, nil)
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.NoError(t, err)
	var usage *types.TokenUsage
	for chunk := range ch {
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	require.NotNil(t, usage)
	assert.Equal(t, 150, usage.PromptTokens, "input + cache read + cache write from message_start")
	assert.Equal(t, 55, usage.CompletionTokens, "cumulative output tokens from message_delta")
	assert.Equal(t, 40, usage.CacheReadTokens)
	assert.Equal(t, 10, usage.CacheWriteTokens)
	assert.True(t, usage.CacheReported)
}

// "messages" is required and must be an array. A conversation that produced no
// turns (only system prompts) used to marshal a nil slice as null.
func TestBuildRequestBody_MessagesNeverNull(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	for _, msgs := range [][]api.Message{nil, {{Role: "system", Content: "be brief"}}} {
		body, err := c.BuildRequestBody(msgs, nil, false)
		require.NoError(t, err)
		data, err := json.Marshal(body)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"messages":[]`)
		assert.NotContains(t, string(data), `"messages":null`)
	}
}

// anthropic-version is mandatory on every request; an empty header is a 400.
func TestSend_DefaultsAnthropicVersion(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL, func(cfg *Config) { cfg.Settings.Version = "" })
	_, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "Hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, defaultAnthropicVersion, got)
}

// storedThinking renders what the client persists on an assistant turn, so
// the replay tests start from the same bytes production would have stored.
func storedThinking(t *testing.T, blocks ...thinkingBlock) types.ProviderMetadata {
	t.Helper()
	raw := thinkingBlocksMetadata(blocks)
	require.NotNil(t, raw)
	return types.ProviderMetadata{MetadataThinkingBlocks: raw}
}

func assistantContent(t *testing.T, body map[string]any) []any {
	t.Helper()
	messages, ok := body["messages"].([]any)
	require.True(t, ok, "messages should be a list")
	require.NotEmpty(t, messages)
	first, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "assistant", first["role"])
	blocks, ok := first["content"].([]any)
	require.True(t, ok, "assistant content should be a block list")
	return blocks
}

func blockTypes(blocks []any) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			out = append(out, m["type"].(string))
		}
	}
	return out
}

func TestInterleavedThinkingReplaysEveryBlockWithItsOwnSignature(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	msg := api.Message{
		Role: "assistant",
		// The concatenation readers see; deliberately different from the
		// signed text below so a replay built from it would be detectable.
		ReasoningContent: "first thoughtsecond thought",
		ReasoningMetadata: storedThinking(t,
			newThinkingBlock("first thought", "sig-1"),
			newThinkingBlock("second thought", "sig-2"),
		),
		ToolCalls: []api.ToolCall{
			{ID: "t1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{"q":"a"}`}},
			{ID: "t2", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{"q":"b"}`}},
		},
	}

	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	require.Equal(t, []string{"thinking", "thinking", "tool_use", "tool_use"}, blockTypes(blocks))

	first := blocks[0].(map[string]any)
	second := blocks[1].(map[string]any)
	assert.Equal(t, "first thought", first["thinking"])
	assert.Equal(t, "sig-1", first["signature"])
	assert.Equal(t, "second thought", second["thinking"])
	assert.Equal(t, "sig-2", second["signature"])
}

func TestReplayKeepsSignedTextWhenReasoningContentWasRewritten(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	// internal/modelcontext rewrites ReasoningContent (resource handles,
	// citation compaction) and clears the now-stale signature. The signed
	// bytes survive in the metadata, so the turn still replays — which
	// matters because dropping the thinking block while keeping tool_use is
	// a 400 from Claude, not a degradation.
	msg := api.Message{
		Role:              "assistant",
		ReasoningContent:  "I read res://0001",
		ReasoningMetadata: storedThinking(t, newThinkingBlock("I read resource://Ab12", "sig-1")),
		ToolCalls: []api.ToolCall{
			{ID: "t1", Type: "function", Function: api.FunctionCall{Name: "read", Arguments: `{}`}},
		},
	}

	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	require.Equal(t, []string{"thinking", "tool_use"}, blockTypes(blocks))
	first := blocks[0].(map[string]any)
	assert.Equal(t, "I read resource://Ab12", first["thinking"], "the signed text, not the rewritten one")
	assert.Equal(t, "sig-1", first["signature"])
}

func TestReplayFallsBackToLegacySingleBlockTurns(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	// Assistant turns stored before the metadata existed carry only the
	// single pair, and must keep replaying.
	msg := api.Message{
		Role:               "assistant",
		ReasoningContent:   "one thought",
		ReasoningSignature: api.TagSignature(api.APIAnthropicMessages, "legacy-sig"),
		ToolCalls: []api.ToolCall{
			{ID: "t1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{}`}},
		},
	}

	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	require.Equal(t, []string{"thinking", "tool_use"}, blockTypes(blocks))
	assert.Equal(t, "legacy-sig", blocks[0].(map[string]any)["signature"])
}

func TestReplayIgnoresAnotherProvidersSignature(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	msg := api.Message{
		Role:               "assistant",
		Content:            "answer",
		ReasoningContent:   "one thought",
		ReasoningSignature: api.TagSignature(api.APIGoogleGenerativeAI, "gemini-sig"),
	}
	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	assert.NotContains(t, blockTypes(blocks), "thinking")
}

func TestStreamKeepsOneSignaturePerThinkingBlock(t *testing.T) {
	s := newStreamState()
	// Two interleaved thinking blocks at different content-block indices.
	s.thinkingAt(0, "thinking").Thinking = "first"
	s.thinkingAt(0, "thinking").Signature = "sig-1"
	s.thinkingAt(2, "thinking").Thinking = "second"
	s.thinkingAt(2, "thinking").Signature = "sig-2"

	blocks := s.thinkingBlocks()
	require.Len(t, blocks, 2)
	assert.Equal(t, thinkingBlock{Type: "thinking", Thinking: "first", Signature: "sig-1"}, blocks[0])
	assert.Equal(t, thinkingBlock{Type: "thinking", Thinking: "second", Signature: "sig-2"}, blocks[1])
}

func TestTruncatedInterleavedTurnStillReplaysItsSignedBlocks(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	// thinking(signed) → tool_use(completed) → thinking(stream cut before
	// signature_delta). Claude rejects an unsigned thinking block, and it
	// also rejects a tool_use that is not preceded by thinking — so the
	// signed block must survive the unsigned one.
	msg := api.Message{
		Role:             "assistant",
		ReasoningContent: "first thoughtcut off",
		ReasoningMetadata: storedThinking(t,
			newThinkingBlock("first thought", "sig-1"),
			newThinkingBlock("cut off", ""),
		),
		ToolCalls: []api.ToolCall{
			{ID: "t1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{}`}},
		},
	}

	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	require.Equal(t, []string{"thinking", "tool_use"}, blockTypes(blocks),
		"a tool_use must still be preceded by a thinking block")
	first := blocks[0].(map[string]any)
	assert.Equal(t, "first thought", first["thinking"])
	assert.Equal(t, "sig-1", first["signature"])
}

func TestUnsignedThinkingIsNeverSentOnItsOwn(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	// A turn whose only thinking block lost its signature: the block cannot
	// be replayed at all, so nothing thinking-shaped goes out.
	msg := api.Message{
		Role:              "assistant",
		Content:           "answer",
		ReasoningContent:  "cut off",
		ReasoningMetadata: storedThinking(t, newThinkingBlock("cut off", "")),
	}

	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	assert.Equal(t, []string{"text"}, blockTypes(blocks))
}

func TestRedactedThinkingSurvivesAnUnsignedNeighbour(t *testing.T) {
	c := newClient(t, "https://api.anthropic.com/v1", nil)
	msg := api.Message{
		Role: "assistant",
		ReasoningMetadata: storedThinking(t,
			newRedactedThinkingBlock("opaque-1"),
			newThinkingBlock("cut off", ""),
		),
		ToolCalls: []api.ToolCall{
			{ID: "t1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{}`}},
		},
	}

	blocks := assistantContent(t, bodyJSON(t, c, []api.Message{msg}, &api.Options{}, false))
	require.Equal(t, []string{"redacted_thinking", "tool_use"}, blockTypes(blocks))
	assert.Equal(t, "opaque-1", blocks[0].(map[string]any)["data"])
}
