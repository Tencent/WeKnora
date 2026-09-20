package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClampPromptCacheKey(t *testing.T) {
	assert.Equal(t, "", ClampPromptCacheKey(""))
	assert.Equal(t, "sess-1", ClampPromptCacheKey("sess-1"))
	long := strings.Repeat("a", 80)
	got := ClampPromptCacheKey(long)
	assert.Equal(t, 64, len([]rune(got)))
	assert.Equal(t, strings.Repeat("a", 64), got)
}

func TestApplyCacheControlBreakpoints(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": "hi"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
	}
	ApplyCacheControlBreakpoints(payload, CacheControlFor(CacheRetentionLong, "1h"))
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	js := string(data)
	assert.Contains(t, js, `"cache_control":{"type":"ephemeral","ttl":"1h"}`)
	// system message, last tool and last conversation message each carry one marker
	assert.Equal(t, 3, strings.Count(js, `"cache_control"`))
}

func TestCacheControlFor_NoneDisables(t *testing.T) {
	assert.Nil(t, CacheControlFor(CacheRetentionNone, "1h"))
	assert.Equal(t, &CacheControlMarker{Type: "ephemeral"}, CacheControlFor(CacheRetentionShort, "1h"))
}

func TestAttachSessionAffinityHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
	require.NoError(t, err)
	AttachSessionAffinityHeaders(req, "sess-1")
	assert.Equal(t, "sess-1", req.Header.Get("session_id"))
	assert.Equal(t, "sess-1", req.Header.Get("x-session-affinity"))
}

// parseUsage decodes a response envelope the way the Chat Completions client
// does, so these cases exercise the same path production takes.
func parseUsage(t *testing.T, body string) (OpenAIUsage, bool) {
	t.Helper()
	var envelope struct {
		Usage *OpenAIUsage `json:"usage"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))
	if envelope.Usage == nil {
		return OpenAIUsage{}, false
	}
	return *envelope.Usage, true
}

func TestOpenAIUsage_CacheVariants(t *testing.T) {
	t.Run("deepseek hit/miss", func(t *testing.T) {
		u, ok := parseUsage(t, `{"usage":{"prompt_tokens":30,"completion_tokens":2,`+
			`"prompt_cache_hit_tokens":20,"prompt_cache_miss_tokens":10}}`)
		require.True(t, ok)
		got := u.ToTokenUsage(true)
		assert.Equal(t, 20, got.CacheReadTokens)
		assert.Equal(t, 10, got.CacheMissTokens)
		assert.True(t, got.CacheReported)
	})
	t.Run("openai details", func(t *testing.T) {
		u, ok := parseUsage(t, `{"usage":{"prompt_tokens":100,"completion_tokens":5,`+
			`"prompt_tokens_details":{"cached_tokens":64}}}`)
		require.True(t, ok)
		got := u.ToTokenUsage(true)
		assert.Equal(t, 64, got.CacheReadTokens)
		assert.Equal(t, 36, got.CacheMissTokens)
	})
	t.Run("kimi top-level cached_tokens", func(t *testing.T) {
		u, ok := parseUsage(t, `{"usage":{"prompt_tokens":100,"completion_tokens":5,"cached_tokens":40}}`)
		require.True(t, ok)
		assert.Equal(t, 40, u.ToTokenUsage(true).CacheReadTokens)
	})
	t.Run("no counters, vendor without accounting", func(t *testing.T) {
		u, ok := parseUsage(t, `{"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
		require.True(t, ok)
		assert.False(t, u.ToTokenUsage(false).CacheReported)
		assert.Equal(t, 15, u.ToTokenUsage(false).TotalTokens)
	})
	t.Run("missing usage", func(t *testing.T) {
		_, ok := parseUsage(t, `{"choices":[]}`)
		assert.False(t, ok)
	})
}

func TestWithSessionCacheKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.SessionIDContextKey, "sess-ctx")
	got := WithSessionCacheKey(ctx, nil)
	require.NotNil(t, got)
	assert.Equal(t, "sess-ctx", got.PromptCacheKey)

	explicit := &Options{PromptCacheKey: "explicit"}
	assert.Same(t, explicit, WithSessionCacheKey(ctx, explicit))

	orig := &Options{Temperature: 0.3}
	got = WithSessionCacheKey(ctx, orig)
	assert.Equal(t, "sess-ctx", got.PromptCacheKey)
	assert.Equal(t, 0.3, got.Temperature)
	assert.Empty(t, orig.PromptCacheKey, "caller options are not mutated")

	assert.Nil(t, WithSessionCacheKey(context.Background(), nil))
}

func TestSignatureTagging(t *testing.T) {
	tagged := TagSignature(APIAnthropicMessages, "abc")
	assert.Equal(t, "abc", SignatureFor(APIAnthropicMessages, tagged))
	assert.Empty(t, SignatureFor(APIGoogleGenerativeAI, tagged))
	assert.Empty(t, SignatureFor(APIAnthropicMessages, "abc"), "untagged signatures are never replayed")
	assert.Empty(t, TagSignature(APIAnthropicMessages, ""))
}
