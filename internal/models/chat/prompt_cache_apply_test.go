package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClampPromptCacheKey(t *testing.T) {
	assert.Equal(t, "", clampPromptCacheKey(""))
	assert.Equal(t, "sess-1", clampPromptCacheKey("sess-1"))
	long := strings.Repeat("a", 80)
	got := clampPromptCacheKey(long)
	assert.Equal(t, 64, len([]rune(got)))
	assert.Equal(t, strings.Repeat("a", 64), got)
}

func TestApplyPromptCacheToJSONBody_OpenAIKey(t *testing.T) {
	req := openai.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
	}
	policy := promptCachePolicyFor(provider.ProviderOpenAI, "")
	body, forceRaw, err := applyPromptCacheToJSONBody(req, policy, "sess-abc", CacheRetentionShort)
	require.NoError(t, err)
	require.True(t, forceRaw)
	payload := body.(map[string]any)
	assert.Equal(t, "sess-abc", payload["prompt_cache_key"])
	assert.NotContains(t, payload, "prompt_cache_retention")
}

func TestApplyPromptCacheToJSONBody_OpenAILongRetention(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "gpt-4o"}
	policy := promptCachePolicyFor(provider.ProviderOpenAI, "")
	body, forceRaw, err := applyPromptCacheToJSONBody(req, policy, "sess-abc", CacheRetentionLong)
	require.NoError(t, err)
	require.True(t, forceRaw)
	payload := body.(map[string]any)
	assert.Equal(t, "24h", payload["prompt_cache_retention"])
}

func TestApplyPromptCacheToJSONBody_NoneLeavesBodyUntouched(t *testing.T) {
	req := &openai.ChatCompletionRequest{Model: "gpt-4o"}
	policy := promptCachePolicyFor(provider.ProviderOpenAI, "")
	body, forceRaw, err := applyPromptCacheToJSONBody(req, policy, "sess-abc", CacheRetentionNone)
	require.NoError(t, err)
	assert.False(t, forceRaw)
	assert.Equal(t, req, body)
}

func TestApplyPromptCacheToJSONBody_AliyunCacheControlBreakpoints(t *testing.T) {
	req := openai.ChatCompletionRequest{
		Model: "qwen-plus",
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: "stable system"},
			{Role: "user", Content: "turn question"},
		},
		Tools: []openai.Tool{{
			Type:     openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{Name: "search", Description: "d", Parameters: json.RawMessage(`{}`)},
		}},
	}
	policy := promptCachePolicyFor(provider.ProviderAliyun, "")
	body, forceRaw, err := applyPromptCacheToJSONBody(req, policy, "", CacheRetentionShort)
	require.NoError(t, err)
	require.True(t, forceRaw)
	wire, err := json.Marshal(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(wire, &payload))

	messages := payload["messages"].([]any)
	sys := messages[0].(map[string]any)
	sysParts := sys["content"].([]any)
	sysCache := sysParts[0].(map[string]any)["cache_control"].(map[string]any)
	assert.Equal(t, "ephemeral", sysCache["type"])

	user := messages[1].(map[string]any)
	userParts := user["content"].([]any)
	userCache := userParts[0].(map[string]any)["cache_control"].(map[string]any)
	assert.Equal(t, "ephemeral", userCache["type"])

	tools := payload["tools"].([]any)
	lastTool := tools[len(tools)-1].(map[string]any)
	toolCache := lastTool["cache_control"].(map[string]any)
	assert.Equal(t, "ephemeral", toolCache["type"])
}

func TestBuildOutbound_OpenAIPromptCacheKeyFromSession(t *testing.T) {
	c := newOutboundChat(t, string(provider.ProviderOpenAI), "gpt-4o", nil)
	ctx := types.WithSessionID(context.Background(), "sess-live")
	body, _, useRaw, err := c.buildOutbound(ctx, []Message{{Role: "user", Content: "hi"}}, &ChatOptions{}, false)
	require.NoError(t, err)
	require.True(t, useRaw)
	payload := body.(map[string]any)
	assert.Equal(t, "sess-live", payload["prompt_cache_key"])
}

func TestAttachPromptCacheHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
	require.NoError(t, err)
	attachPromptCacheHeaders(req, promptCachePolicy{sendAffinity: true}, "sess-1")
	assert.Equal(t, "sess-1", req.Header.Get("session_id"))
	assert.Equal(t, "sess-1", req.Header.Get("x-client-request-id"))
	assert.Equal(t, "sess-1", req.Header.Get("x-session-affinity"))
}
