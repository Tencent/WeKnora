package runtime_test

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerTestVendors(t *testing.T) {
	t.Helper()
	before := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(before) })
	modelruntime.Register(&providers.Definition{
		ID: providers.GenericID, Name: "Custom", API: api.APIOpenAICompletions,
		DefaultBaseURLs: map[types.ModelType]string{},
		ModelTypes:      []types.ModelType{types.ModelTypeKnowledgeQA},
		Compat: providers.VendorCompat{OpenAICompletions: api.OpenAICompletionsCompat{
			MaxTokensField: api.Ptr("max_tokens"),
			ThinkingFormat: api.Ptr(api.ThinkingFormatChatTemplateKwargs),
		}},
	})
	modelruntime.Register(&providers.Definition{
		ID: "acme", Name: "Acme", Names: map[string]string{"zh-CN": "极目"},
		API:          api.APIOpenAICompletions,
		RequiresAuth: true,
		URLPatterns:  []string{"api.acme.ai", "acme"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: "https://api.acme.ai/v1",
			types.ModelTypeRerank:      "https://api.acme.ai/rerank",
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeRerank},
		Compat: providers.VendorCompat{OpenAICompletions: api.OpenAICompletionsCompat{
			ThinkingFormat:          api.Ptr(api.ThinkingFormatThinkingType),
			SupportsReasoningEffort: api.Ptr(true),
			MaxTokensField:          api.Ptr("max_tokens"),
		}},
		ThinkingLevels: api.ThinkingLevelMap{api.ReasoningMax: api.StringPtr("max")},
	}, []models.ModelSpec{
		{
			ID: "acme-pro", Name: "Acme Pro", Reasoning: true,
			Input: []string{"text", "image"}, ContextWindow: 200000,
			Compat: json.RawMessage(`{"supports_temperature": false}`),
		},
		{ID: "acme-r1", Reasoning: true, ThinkingLevels: api.ThinkingLevelMap{api.ReasoningOff: nil}},
		{Match: "acme-lite*", Reasoning: false, Compat: json.RawMessage(`{"thinking_format": "none"}`)},
		{Match: "acme-lite-vision*", Input: []string{"text", "image"}},
		{ID: "acme-embed", Type: types.ModelTypeEmbedding, Dimension: 1024},
		{
			ID: "acme-claude", API: api.APIAnthropicMessages, Reasoning: true,
			Compat: json.RawMessage(`{"thinking_mode": "adaptive", "supports_effort": true}`),
		},
	}...)
	modelruntime.Register(&providers.Definition{
		ID: "tencent-lkeap", Name: "LKEAP", API: api.APIOpenAICompletions,
		URLPatterns: []string{"api.lkeap.cloud.tencent.com", "tencent"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: "https://api.lkeap.cloud.tencent.com/v1",
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
	})
}

func TestFindModel_ExactAliasAndGlob(t *testing.T) {
	registerTestVendors(t)
	v, _ := modelruntime.Get("acme")

	chat := types.ModelTypeKnowledgeQA
	m, ok := v.FindModel("ACME-PRO", chat)
	require.True(t, ok)
	assert.Equal(t, "acme-pro", m.ID)

	m, ok = v.FindModel("acme-lite-2", chat)
	require.True(t, ok)
	assert.Equal(t, "acme-lite*", m.Match)

	m, ok = v.FindModel("acme-lite-vision-2", chat)
	require.True(t, ok, "longest literal prefix wins")
	assert.Equal(t, "acme-lite-vision*", m.Match)

	_, ok = v.FindModel("unknown", chat)
	assert.False(t, ok)
}

// A lookup only sees entries of the row's own type. A VLM row is a chat
// model that accepts images, so it sees the chat entries.
func TestFindModel_IsTyped(t *testing.T) {
	registerTestVendors(t)
	v, _ := modelruntime.Get("acme")

	_, ok := v.FindModel("acme-embed", types.ModelTypeKnowledgeQA)
	assert.False(t, ok, "a chat row must not pick up an embedding entry")
	m, ok := v.FindModel("acme-embed", types.ModelTypeEmbedding)
	require.True(t, ok)
	assert.Equal(t, "acme-embed", m.ID)

	_, ok = v.FindModel("acme-lite-embed", types.ModelTypeEmbedding)
	assert.False(t, ok, "an embedding row must not pick up a chat glob")
	_, ok = v.FindModel("acme-lite-2", types.ModelTypeVLLM)
	assert.True(t, ok)
}

// The failure a typed lookup prevents: a chat family's glob carries chat
// compat, and handing it to an embedding or rerank row fails the strict
// overlay decode, so the row could not be built at all.
func TestResolve_ModelNameInsideAChatGlobStillResolvesForOtherTypes(t *testing.T) {
	registerTestVendors(t)
	for _, modelType := range []types.ModelType{types.ModelTypeEmbedding, types.ModelTypeRerank} {
		r, err := modelruntime.Resolve(
			modelruntime.Ref{
				Provider:  "acme",
				Model:     "acme-lite-embed",
				ModelType: modelType,
			},
		)
		require.NoError(t, err, modelType)
		assert.False(t, r.Cataloged, modelType)
	}
}

func TestDetectByURL_LongestPatternWins(t *testing.T) {
	registerTestVendors(t)
	assert.Equal(t, "tencent-lkeap", modelruntime.DetectByURL("https://api.lkeap.cloud.tencent.com/v1"))
	assert.Equal(t, "acme", modelruntime.DetectByURL("https://api.acme.ai/v1"))
	assert.Equal(t, providers.GenericID, modelruntime.DetectByURL("http://localhost:8000/v1"))
	assert.Equal(t, providers.GenericID, modelruntime.DetectByURL(""))
}

func TestResolve_MergesVendorModelAndOverrideLayers(t *testing.T) {
	registerTestVendors(t)

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro"})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.Equal(t, api.APIOpenAICompletions, r.API)
	assert.Equal(t, "https://api.acme.ai/v1", r.BaseURL, "default base URL is filled in")
	assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField)
	assert.Equal(t, api.ThinkingFormatThinkingType, r.OpenAICompletions.ThinkingFormat)
	assert.True(t, r.OpenAICompletions.SupportsReasoningEffort)
	assert.False(t, r.OpenAICompletions.SupportsTemperature, "model compat overrides the protocol default")
	assert.True(t, r.OpenAICompletions.SupportsUsageInStreaming, "untouched defaults survive")
	assert.Equal(t, "max", r.ThinkingLevels.Value(api.ReasoningMax))
	assert.True(t, r.ThinkingLevels.Supports(api.ReasoningMax))
	assert.Equal(t, 200000, r.Spec.ContextWindow)

	caps := r.Capabilities()
	assert.True(t, caps.Reasoning)
	assert.Equal(t, []string{"text", "image"}, caps.Input)
	assert.Contains(t, caps.ThinkingLevels, api.ReasoningOff)
	assert.Contains(t, caps.ThinkingLevels, api.ReasoningMax)

	// Row-level override wins over everything.
	on := true
	r, err = modelruntime.Resolve(modelruntime.Ref{
		Provider: "acme", Model: "acme-pro", BaseURL: "https://proxy.example.com/v1/",
		Override: &types.ModelSpecOverride{
			ContextWindow:  1000,
			Reasoning:      &on,
			ThinkingLevels: map[string]*string{"off": nil},
			Compat:         map[string]any{"supports_temperature": true, "max_tokens_field": "max_completion_tokens"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://proxy.example.com/v1", r.BaseURL, "trailing slash trimmed, override kept")
	assert.Equal(t, 1000, r.Spec.ContextWindow)
	assert.True(t, r.OpenAICompletions.SupportsTemperature)
	assert.Equal(t, "max_completion_tokens", r.OpenAICompletions.MaxTokensField)
	assert.False(t, r.ThinkingLevels.Supports(api.ReasoningOff))
}

func TestResolve_UnknownModelAndVendorDegradeToGeneric(t *testing.T) {
	registerTestVendors(t)

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "brand-new"})
	require.NoError(t, err)
	assert.False(t, r.Cataloged)
	assert.Equal(t, "brand-new", r.Spec.ID)
	assert.Equal(
		t,
		api.ThinkingFormatThinkingType,
		r.OpenAICompletions.ThinkingFormat,
		"vendor defaults still apply",
	)

	r, err = modelruntime.Resolve(modelruntime.Ref{Provider: "nope", Model: "x", BaseURL: "http://localhost:8000/v1"})
	require.NoError(t, err)
	assert.Equal(t, providers.GenericID, r.Vendor.ID)
	assert.Equal(t, api.ThinkingFormatChatTemplateKwargs, r.OpenAICompletions.ThinkingFormat)

	r, err = modelruntime.Resolve(modelruntime.Ref{Model: "x", BaseURL: "https://api.acme.ai/v1"})
	require.NoError(t, err)
	assert.Equal(t, "acme", r.Vendor.ID, "provider detected from URL when empty")
}

func TestResolve_ProtocolInference(t *testing.T) {
	registerTestVendors(t)

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-claude"})
	require.NoError(t, err)
	assert.Equal(t, api.APIAnthropicMessages, r.API)
	assert.Equal(t, api.AnthropicThinkingAdaptive, r.AnthropicMessages.ThinkingMode)
	assert.True(t, r.AnthropicMessages.SupportsEffort)
	assert.Equal(t, "thinking.adaptive", r.Capabilities().ThinkingFormat)

	r, err = modelruntime.Resolve(
		modelruntime.Ref{
			Provider: "acme",
			Model:    "acme-pro",
			BaseURL:  "https://api.acme.ai/anthropic",
		},
	)
	require.NoError(t, err)
	assert.Equal(t, api.APIAnthropicMessages, r.API, "/anthropic suffix switches protocol")

	r, err = modelruntime.Resolve(
		modelruntime.Ref{
			Provider: "acme",
			Model:    "acme-pro",
			Extra: map[string]string{
				models.ExtraAPI: "openai-responses",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, api.APIOpenAIResponses, r.API, "extra_config.api forces the protocol")

	_, err = modelruntime.Resolve(
		modelruntime.Ref{
			Provider: "acme",
			Model:    "acme-pro",
			Extra: map[string]string{
				models.ExtraAPI: "bogus",
			},
		},
	)
	require.Error(t, err)
}

func TestResolve_LegacyThinkingControlAndRemoteModelName(t *testing.T) {
	registerTestVendors(t)
	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro", Extra: map[string]string{
		models.ExtraThinkingControl: "enable_thinking",
		models.ExtraRemoteModelName: "acme-pro-2026",
	}})
	require.NoError(t, err)
	assert.Equal(t, api.ThinkingFormatEnableThinking, r.OpenAICompletions.ThinkingFormat)
	assert.Equal(t, "acme-pro-2026", r.RemoteModel)

	r, err = modelruntime.Resolve(
		modelruntime.Ref{
			Provider: "acme",
			Model:    "acme-pro",
			Extra: map[string]string{
				models.ExtraThinkingControl: "none",
			},
		},
	)
	require.NoError(t, err)
	assert.Empty(t, r.Capabilities().ThinkingLevels, "none disables the selector")
}

func TestResolve_AlwaysOnModelCannotBeSwitchedOff(t *testing.T) {
	registerTestVendors(t)
	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-r1"})
	require.NoError(t, err)
	assert.False(t, r.ThinkingLevels.Supports(api.ReasoningOff))
	assert.NotContains(t, r.Capabilities().ThinkingLevels, api.ReasoningOff)
}

func TestResolve_RejectsUnknownCompatKeys(t *testing.T) {
	registerTestVendors(t)
	_, err := modelruntime.Resolve(
		modelruntime.Ref{
			Provider: "acme",
			Model:    "acme-pro",
			Override: &types.ModelSpecOverride{
				Compat: map[string]any{"max_token_field": "max_tokens"},
			},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_token_field")
}

func TestModelsByType(t *testing.T) {
	registerTestVendors(t)
	v, _ := modelruntime.Get("acme")
	chat := v.ModelsByType(types.ModelTypeKnowledgeQA)
	ids := make([]string, 0, len(chat))
	for _, m := range chat {
		ids = append(ids, m.ID)
	}
	assert.ElementsMatch(t, []string{"acme-pro", "acme-r1", "acme-claude"}, ids, "pattern-only entries are hidden")
	vlm := v.ModelsByType(types.ModelTypeVLLM)
	require.Len(t, vlm, 1)
	assert.Equal(t, "acme-pro", vlm[0].ID, "VLM picker lists chat models with image input")
	embed := v.ModelsByType(types.ModelTypeEmbedding)
	require.Len(t, embed, 1)
	assert.Equal(t, 1024, embed[0].Dimension)
}

func TestApplyOverlay(t *testing.T) {
	registerTestVendors(t)
	t.Setenv("ACME_KEY", "sk-from-env")
	overlay := `{
	  "providers": {
	    "acme": {
	      "base_url": "https://acme.internal/v1",
	      "api_key": "${ACME_KEY}",
	      "headers": {"X-Team": "$ACME_KEY"},
	      "thinking_levels": {"xhigh": "max"},
	      "models": [{"id": "acme-ultra", "reasoning": true, "context_window": 400000}],
	      "model_overrides": {"acme-pro": {"context_window": 123, "compat": {"supports_temperature": true}}}
	    },
	    "my-vllm": {
	      "name": "Lab vLLM",
	      "names": {"zh-CN": "实验室 vLLM"},
	      "base_url": "http://vllm.lab:8000/v1",
	      "requires_auth": false,
	      "compat": {"thinking_format": "chat-template-kwargs", "max_tokens_field": "max_tokens"},
	      "models": [{"id": "Qwen/Qwen3-32B", "reasoning": true}]
	    }
	  }
	}`
	require.NoError(t, modelruntime.ApplyOverlay([]byte(overlay), t.TempDir()))

	acme, ok := modelruntime.Get("acme")
	require.True(t, ok)
	assert.Equal(t, "https://acme.internal/v1", acme.GetDefaultURL(types.ModelTypeKnowledgeQA))
	assert.Equal(t, "https://api.acme.ai/rerank", acme.GetDefaultURL(types.ModelTypeRerank), "other URLs untouched")
	assert.Equal(t, "sk-from-env", acme.DefaultAPIKey)
	assert.Equal(t, "sk-from-env", acme.Headers["X-Team"])
	assert.Equal(t, "max", acme.ThinkingLevels.Value(api.ReasoningXHigh))
	assert.Equal(t, "max", acme.ThinkingLevels.Value(api.ReasoningMax), "existing levels kept")

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-ultra"})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.Equal(t, 400000, r.Spec.ContextWindow)

	r, err = modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro"})
	require.NoError(t, err)
	assert.Equal(t, 123, r.Spec.ContextWindow)
	assert.True(t, r.OpenAICompletions.SupportsTemperature, "model_overrides compat merges over the built-in compat")

	lab, ok := modelruntime.Get("my-vllm")
	require.True(t, ok)
	assert.Equal(t, "实验室 vLLM", lab.LocalizedName("zh-CN"))
	assert.Equal(t, "Lab vLLM", lab.LocalizedName("en-US"))
	assert.False(t, lab.RequiresAuth)
	r, err = modelruntime.Resolve(modelruntime.Ref{Provider: "my-vllm", Model: "Qwen/Qwen3-32B"})
	require.NoError(t, err)
	assert.Equal(t, api.ThinkingFormatChatTemplateKwargs, r.OpenAICompletions.ThinkingFormat)
	assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField)
	assert.True(t, r.Spec.Reasoning)
	assert.Equal(t, "http://vllm.lab:8000/v1", r.BaseURL)

	assert.Error(
		t,
		modelruntime.ApplyOverlay(
			[]byte(
				`{"providers": {"x": {"bogus": 1}}}`,
			),
			t.TempDir(),
		),
		"unknown keys rejected",
	)
	assert.Error(t, modelruntime.ApplyOverlay([]byte(`{"providers": {"x": {"api": "nope"}}}`), t.TempDir()))
}

func TestThinkingLevelMap(t *testing.T) {
	m := api.ThinkingLevelMap{
		api.ReasoningMedium: nil,
		api.ReasoningMax:    api.StringPtr("max"),
		api.ReasoningOff:    api.StringPtr("none"),
	}
	assert.False(t, m.Supports(api.ReasoningMedium))
	assert.True(t, m.Supports(api.ReasoningMax))
	assert.False(t, m.Supports(api.ReasoningXHigh), "xhigh needs an explicit entry")
	assert.Equal(t, api.ReasoningHigh, m.Clamp(api.ReasoningMedium), "clamps upward first")
	assert.Equal(t, api.ReasoningMax, m.Clamp(api.ReasoningXHigh))
	assert.Equal(t, "none", m.Value(api.ReasoningOff))
	assert.Equal(t, "low", m.Value(api.ReasoningLow), "unmapped levels pass through")
	assert.Equal(t, []api.ReasoningEffort{
		api.ReasoningOff, api.ReasoningAuto, api.ReasoningMinimal,
		api.ReasoningLow, api.ReasoningHigh, api.ReasoningMax,
	}, m.SupportedLevels())

	level, ok := api.ParseReasoningEffort("true")
	assert.True(t, ok)
	assert.Equal(t, api.ReasoningAuto, level)
	_, ok = api.ParseReasoningEffort("ultra")
	assert.False(t, ok)
}
