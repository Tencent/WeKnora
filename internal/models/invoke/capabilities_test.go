package invoke

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolFor(t *testing.T) {
	assert.Equal(t, ProtocolAnthropicMessages, protocolFor(ProviderAnthropic))
	// gemini rides the official OpenAI-compat layer (裁定 #31) — wire family is
	// the standard OpenAI Chat shape now.
	assert.Equal(t, ProtocolOpenAIChat, protocolFor(ProviderGemini))
	// OpenAI-compatible family is the default for everything else.
	assert.Equal(t, ProtocolOpenAIChat, protocolFor(ProviderOpenAI))
	assert.Equal(t, ProtocolOpenAIChat, protocolFor(ProviderGeneric))
	assert.Equal(t, ProtocolOpenAIChat, protocolFor(ProviderAliyun))
}

func TestThinkingCapsFor(t *testing.T) {
	t.Run("standard three-level vendors", func(t *testing.T) {
		for _, name := range []ProviderName{
			ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderAliyun, ProviderDeepSeek,
		} {
			caps := thinkingCapsFor(name)
			assert.True(t, caps.Supported, "%s supports thinking", name)
			assert.True(t, caps.CanDisable, "%s can disable thinking", name)
			assert.Equal(t, []Level{LevelLow, LevelMedium, LevelHigh}, caps.SupportedLevels)
			assert.Equal(t, LevelMedium, caps.DefaultLevel)
			assert.Contains(t, caps.SupportedLevels, caps.DefaultLevel, "DefaultLevel must be in SupportedLevels")
		}
	})

	t.Run("volcengine has xhigh tier", func(t *testing.T) {
		caps := thinkingCapsFor(ProviderVolcengine)
		assert.Contains(t, caps.SupportedLevels, LevelXHigh)
		assert.Contains(t, caps.SupportedLevels, caps.DefaultLevel)
	})

	t.Run("unknown vendor exposes no provider-level thinking", func(t *testing.T) {
		caps := thinkingCapsFor(ProviderJina) // embedding/rerank only
		assert.False(t, caps.Supported)
		assert.Empty(t, caps.SupportedLevels)
	})
}

func TestDefaultCapabilitiesShardsByModelType(t *testing.T) {
	t.Run("chat vendor gets Chat shard only", func(t *testing.T) {
		caps := defaultCapabilities(ProviderOpenAI, []types.ModelType{types.ModelTypeKnowledgeQA})
		require.NotNil(t, caps.Chat)
		assert.Nil(t, caps.Embedding)
		assert.Nil(t, caps.Rerank)
		assert.Nil(t, caps.ASR)
		assert.True(t, caps.Chat.Thinking.Supported)
		assert.Equal(t, ProtocolOpenAIChat, caps.Chat.Protocol)
		assert.Equal(t, UsageFull, caps.Common.UsageReporting)
	})

	t.Run("vllm shares the Chat shard", func(t *testing.T) {
		caps := defaultCapabilities(ProviderOpenAI, []types.ModelType{types.ModelTypeVLLM})
		require.NotNil(t, caps.Chat, "VLLM shares the Chat capability shard")
	})

	t.Run("embedding-only vendor gets no Chat shard", func(t *testing.T) {
		caps := defaultCapabilities(ProviderJina, []types.ModelType{types.ModelTypeEmbedding, types.ModelTypeRerank})
		assert.Nil(t, caps.Chat)
		require.NotNil(t, caps.Embedding)
		require.NotNil(t, caps.Rerank)
		assert.False(t, caps.Chat == nil && caps.Embedding == nil, "must declare its served shards")
	})

	t.Run("full vendor declares every served shard", func(t *testing.T) {
		caps := defaultCapabilities(ProviderAliyun, []types.ModelType{
			types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeVLLM,
		})
		assert.NotNil(t, caps.Chat)
		assert.NotNil(t, caps.Embedding)
		assert.NotNil(t, caps.Rerank)
		assert.Nil(t, caps.ASR) // not served
	})
}

func TestEffectiveCapabilities(t *testing.T) {
	t.Run("synthesizes when unset", func(t *testing.T) {
		info := ProviderInfo{
			Name:       ProviderOpenAI,
			ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
		}
		caps := info.EffectiveCapabilities()
		require.NotNil(t, caps.Chat)
		assert.True(t, caps.Chat.Thinking.Supported)
	})

	t.Run("explicit declaration wins over synthesis", func(t *testing.T) {
		custom := Capabilities{
			Chat: &ChatCaps{
				Thinking: ThinkingCaps{
					Supported:       true,
					CanDisable:      false,
					SupportedLevels: []Level{LevelHigh, LevelMax},
					DefaultLevel:    LevelMax,
				},
				Protocol: ProtocolOpenAIChat,
			},
		}
		info := ProviderInfo{
			Name:         ProviderGeneric,
			ModelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
			Capabilities: custom,
		}
		got := info.EffectiveCapabilities()
		require.NotNil(t, got.Chat)
		assert.False(t, got.Chat.Thinking.CanDisable, "explicit CanDisable=false preserved")
		assert.Equal(t, []Level{LevelHigh, LevelMax}, got.Chat.Thinking.SupportedLevels)
		assert.Equal(t, LevelMax, got.Chat.Thinking.DefaultLevel)
	})
}

func TestCapabilitiesIsZero(t *testing.T) {
	assert.True(t, Capabilities{}.isZero())
	assert.False(t, Capabilities{Chat: &ChatCaps{}}.isZero())
	assert.False(t, Capabilities{Common: CommonCaps{Streaming: true}}.isZero())
}

// TestRegisteredProvidersHaveUsableCapabilities is the integration guard: every
// registered provider must yield a non-empty EffectiveCapabilities whose Chat
// shard (when present) has a DefaultLevel inside SupportedLevels — the invariant
// the frontend dropdown and the cross-vendor fallback both rely on.
