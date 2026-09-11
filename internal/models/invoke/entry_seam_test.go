package invoke

// entry_seam_test.go — P1c seams ②③④ at the entry layer:
//   - ② usage frames ride the final Done chunk (never standalone) and
//     interrupted streams deliver the partial tool calls;
//   - ③ Usage carries prompt-cache detail (ApplyRawPromptCacheUsage);
//   - ④ the tier chain folds into ChatOptions.ThinkingLevel before dispatch,
//     and ExtraConfig overrides fold into Endpoint.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capsFakeAdapter is a fakeChatAdapter with a declared thinking capability so
// the entry fold exercises the provider-default tier.
type capsFakeAdapter struct {
	fakeChatAdapter
	caps ThinkingCaps
}

func (f capsFakeAdapter) Capabilities() Capabilities {
	return Capabilities{Chat: &ChatCaps{Thinking: f.caps}}
}

func thinkingCaps(levels ...Level) ThinkingCaps {
	return ThinkingCaps{
		Supported: true, CanDisable: true,
		SupportedLevels: levels, DefaultLevel: LevelMedium,
	}
}

// TestFoldChatOptionsTierChain covers the four-tier priority matrix (seam ④):
// callLevel > model record (ThinkingLevel within SelectedLevels) > provider
// DefaultLevel > none.
func TestFoldChatOptionsTierChain(t *testing.T) {
	caps := thinkingCaps(LevelLow, LevelMedium, LevelHigh)
	registerFake(t, capsFakeAdapter{caps: caps})
	m := &ModelConfig{
		Provider: "fake", ThinkingLevel: "low", SelectedLevels: []string{"low", "high"},
	}

	fold := func(callLevel string, model *ModelConfig) string {
		return foldChatOptions(model, &ChatOptions{ThinkingLevel: callLevel}).ThinkingLevel
	}

	// Tier 1: a call level within both sets wins untouched (no copy — same
	// pointer).
	opts := &ChatOptions{ThinkingLevel: "high"}
	folded := foldChatOptions(m, opts)
	assert.Equal(t, "high", folded.ThinkingLevel)
	assert.Same(t, opts, folded)

	// Tier 1 fails (xhigh unsupported by the provider) → tier 2: the model
	// record level (within the selection set).
	assert.Equal(t, "low", fold("xhigh", m))

	// Tier 2 fails (record empty) → tier 3: the provider default.
	assert.Equal(t, "medium", fold("xhigh", &ModelConfig{Provider: "fake"}))

	// Tier 2 fails on the selection set AND the set excludes the provider
	// default → no level parameter at all.
	badSet := &ModelConfig{Provider: "fake", ThinkingLevel: "high", SelectedLevels: []string{"low"}}
	assert.Equal(t, "", fold("xhigh", badSet))
}

func TestFoldThinkingControl(t *testing.T) {
	cases := map[string]string{
		"":                     "", // unset → adapter default
		"none":                 "none",
		"enable_thinking":      "enable_thinking",
		"thinking_type":        "thinking_type",
		"chat_template_kwargs": "chat_template_kwargs",
		"  Thinking_Type  ":    "thinking_type",        // trim + case fold
		"whatever":             "chat_template_kwargs", // unknown → legacy default
	}
	for in, want := range cases {
		assert.Equal(t, want, foldThinkingControl(map[string]string{"thinking_control": in}), "in=%q", in)
	}
	assert.Equal(t, "", foldThinkingControl(nil))
}

func TestEndpointFoldsExtraConfig(t *testing.T) {
	m := &ModelConfig{BaseURL: "http://x", ExtraConfig: map[string]string{
		"api_version":      "2024-10-21",
		"thinking_control": "none",
	}}
	ep := endpoint(m)
	assert.Equal(t, "2024-10-21", ep.APIVersion)
	assert.Equal(t, "none", ep.ThinkingControl)

	// No ExtraConfig → both overrides empty (adapter defaults apply).
	assert.Zero(t, endpoint(&ModelConfig{}).APIVersion)
	assert.Zero(t, endpoint(&ModelConfig{}).ThinkingControl)
}

// TestChatStreamUsageRidesDoneChunk (seam ②): the usage frame must not be
// delivered as a standalone chunk; it merges into the final Done chunk.
func TestChatStreamUsageRidesDoneChunk(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(
			sseChunk(t, map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"content": "hi"},
			}}}) +
				sseChunk(t, map[string]any{"choices": []any{}, "usage": map[string]any{
					"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7,
				}}) +
				sseChunk(t, map[string]any{"choices": []any{map[string]any{
					"delta": map[string]any{}, "finish_reason": "stop",
				}}}) +
				"data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	ch, err := ChatStream(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	require.NoError(t, err)
	var chunks []types.StreamResponse
	for resp := range ch {
		chunks = append(chunks, resp)
	}
	for _, c := range chunks {
		assert.False(t, !c.Done && c.Usage != nil, "standalone usage chunk delivered: %+v", c)
	}
	var done []types.StreamResponse
	for _, c := range chunks {
		if c.Done {
			done = append(done, c)
		}
	}
	require.Len(t, done, 1)
	require.NotNil(t, done[0].Usage)
	assert.Equal(t, 3, done[0].Usage.PromptTokens)
	assert.Equal(t, 7, done[0].Usage.TotalTokens)
	assert.Equal(t, "stop", done[0].FinishReason)
}

// TestChatStreamInterruptedCarriesPartialToolCalls (seam ②): a mid-stream
// break surfaces the assembled partial tool calls with FinishReasonIncomplete.
func TestChatStreamInterruptedCarriesPartialToolCalls(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		chunk := func(tc map[string]any) string {
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{tc}},
			}}})
			return "data: " + string(data) + "\n\n"
		}
		_, _ = w.Write([]byte(
			chunk(map[string]any{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": ""},
			}) +
				chunk(map[string]any{
					"index":    0,
					"function": map[string]any{"arguments": `{"city":`},
				})))
		f.Flush()
		// Abrupt connection drop: hijack and close.
		hj := w.(http.Hijacker)
		conn, _, err := hj.Hijack()
		require.NoError(t, err)
		_ = conn.Close()
	}))
	defer srv.Close()
	registerFake(t, fakeChatAdapter{baseURL: srv.URL})

	ch, err := ChatStream(context.Background(), testModelConfig(srv.URL), &ChatOptions{})
	require.NoError(t, err)
	var errChunk *types.StreamResponse
	for resp := range ch {
		if resp.ResponseType == types.ResponseTypeError {
			sr := resp
			errChunk = &sr
		}
	}
	require.NotNil(t, errChunk, "interrupted stream must emit an error chunk")
	assert.Equal(t, types.FinishReasonIncomplete, errChunk.FinishReason)
	require.Len(t, errChunk.ToolCalls, 1)
	assert.Equal(t, "call_1", errChunk.ToolCalls[0].ID)
	assert.Equal(t, "get_weather", errChunk.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"city":`, errChunk.ToolCalls[0].Function.Arguments)
}

// TestApplyRawPromptCacheUsage (seam ③): the v1 raw-counter semantics.
func TestApplyRawPromptCacheUsage(t *testing.T) {
	// DeepSeek hit/miss.
	u := Usage{PromptTokens: 4096}
	deepseekBody := []byte(`{"usage":{"prompt_tokens":4096,` +
		`"prompt_cache_hit_tokens":3072,"prompt_cache_miss_tokens":1024}}`)
	ApplyRawPromptCacheUsage(deepseekBody, &u)
	assert.Equal(t, 3072, u.CacheReadTokens)
	assert.Equal(t, 1024, u.CacheMissTokens)
	assert.True(t, u.CacheReported)

	// Anthropic-style read/creation.
	u = Usage{PromptTokens: 100}
	anthropicBody := []byte(`{"usage":{"prompt_tokens":100,` +
		`"cache_read_input_tokens":80,"cache_creation_input_tokens":10}}`)
	ApplyRawPromptCacheUsage(anthropicBody, &u)
	assert.Equal(t, 80, u.CacheReadTokens)
	assert.Equal(t, 10, u.CacheWriteTokens)
	assert.Equal(t, 20, u.CacheMissTokens)
	assert.True(t, u.CacheReported)

	// OpenAI prompt_tokens_details.
	u = Usage{PromptTokens: 50}
	openAIBody := []byte(`{"usage":{"prompt_tokens":50,` +
		`"prompt_tokens_details":{"cached_tokens":30}}}`)
	ApplyRawPromptCacheUsage(openAIBody, &u)
	assert.Equal(t, 30, u.CacheReadTokens)
	assert.True(t, u.CacheReported)

	// Nothing reported → untouched.
	u = Usage{PromptTokens: 5}
	ApplyRawPromptCacheUsage([]byte(`{"usage":{"prompt_tokens":5}}`), &u)
	assert.False(t, u.CacheReported)
}

// TestStreamBridgeBackfillsCacheUsageOnUsageFrame: the openai-shape bridge
// captures native counters on usage-only frames (seam ③ stream side).
func TestStreamBridgeBackfillsCacheUsageOnUsageFrame(t *testing.T) {
	ev, err := OpenAIStreamBridge{}.TranslateStreamEvent(NewStreamBridgeState(), StreamChunk{Data: []byte(
		`{"choices":[],"usage":{"prompt_tokens":4096,"completion_tokens":4,` +
			`"total_tokens":4100,"prompt_cache_hit_tokens":3072,"prompt_cache_miss_tokens":1024}}`)})
	require.NoError(t, err)
	require.Equal(t, StreamKindUsage, ev.Kind)
	require.NotNil(t, ev.Usage)
	assert.Equal(t, 3072, ev.Usage.CacheReadTokens)
	assert.Equal(t, 1024, ev.Usage.CacheMissTokens)
	assert.True(t, ev.Usage.CacheReported)
}
