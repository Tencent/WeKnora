package runtime_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #3551: the chat_template_kwargs wire format only transmits
// enable_thinking, so advertising the full graded ladder for providers that
// use it (generic/gpustack/nvidia) let the UI offer levels the wire silently
// drops. Without explicit graded levels only off/auto may be advertised.
func TestCapabilities_ChatTemplateKwargsWithoutGradedLevelsAdvertisesToggleOnly(t *testing.T) {
	registerTestVendors(t)

	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: providers.GenericID, Model: "local-qwen",
	})
	require.NoError(t, err)
	assert.Equal(t, api.ThinkingFormatChatTemplateKwargs, r.OpenAICompletions.ThinkingFormat)

	caps := r.Capabilities()
	assert.Contains(t, caps.ThinkingLevels, api.ReasoningOff)
	assert.Contains(t, caps.ThinkingLevels, api.ReasoningAuto)
	assert.NotContains(t, caps.ThinkingLevels, api.ReasoningMinimal)
	assert.NotContains(t, caps.ThinkingLevels, api.ReasoningLow)
	assert.NotContains(t, caps.ThinkingLevels, api.ReasoningMedium)
	assert.NotContains(t, caps.ThinkingLevels, api.ReasoningHigh)
}

// An operator who configures explicit graded levels (with vendor mappings)
// still gets them advertised: the map carries real information then.
func TestCapabilities_ChatTemplateKwargsWithExplicitGradedLevelsKeepsLadder(t *testing.T) {
	registerTestVendors(t)
	modelruntime.Register(&providers.Definition{
		ID: "ctk-graded", Name: "CTK Graded", API: api.APIOpenAICompletions,
		DefaultBaseURLs: map[types.ModelType]string{},
		ModelTypes:      []types.ModelType{types.ModelTypeKnowledgeQA},
		ThinkingLevels:  api.ThinkingLevelMap{api.ReasoningHigh: api.StringPtr("high")},
		Compat: providers.VendorCompat{OpenAICompletions: api.OpenAICompletionsCompat{
			ThinkingFormat: api.Ptr(api.ThinkingFormatChatTemplateKwargs),
		}},
	})

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "ctk-graded", Model: "m1"})
	require.NoError(t, err)
	assert.Equal(t, api.ThinkingFormatChatTemplateKwargs, r.OpenAICompletions.ThinkingFormat)

	caps := r.Capabilities()
	assert.Contains(t, caps.ThinkingLevels, api.ReasoningHigh)
}

// Other thinking formats are unaffected: the acme vendor advertises its
// configured ladder as before.
func TestCapabilities_NonChatTemplateKwargsFormatsUnaffected(t *testing.T) {
	registerTestVendors(t)

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro"})
	require.NoError(t, err)
	caps := r.Capabilities()
	assert.NotEqual(t, api.ThinkingFormatChatTemplateKwargs, api.ThinkingFormat(caps.ThinkingFormat))
	assert.Contains(t, caps.ThinkingLevels, api.ReasoningMax)
}
