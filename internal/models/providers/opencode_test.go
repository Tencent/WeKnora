package providers_test

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpencodePresets(t *testing.T) {
	for _, tc := range []struct {
		id, base string
	}{
		{providers.OpencodeZenID, "https://opencode.ai/inference/openai/v1"},
		{providers.OpencodeGoID, "https://opencode.ai/inference/go/openai/v1"},
	} {
		v, ok := modelruntime.Get(tc.id)
		require.True(t, ok, tc.id)
		assert.Equal(t, []types.ModelType{types.ModelTypeKnowledgeQA}, v.ModelTypes, tc.id)
		assert.Equal(t, tc.base, v.GetDefaultURL(types.ModelTypeKnowledgeQA), tc.id)
		assert.Equal(t, providers.AuthBearer, v.Auth, tc.id)
		assert.True(t, v.RequiresAuth, tc.id)
		assert.Error(t, v.ValidateConfig(&providers.Config{ModelName: "glm-5.3"}), tc.id)
		assert.NoError(t, v.ValidateConfig(&providers.Config{ModelName: "glm-5.3", APIKey: "k"}), tc.id)
		assert.NotEmpty(t, v.Models(), tc.id)
	}
}

// Both gateways share opencode.ai; Go is told apart by its path segment.
func TestOpencodeDetectByURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://opencode.ai/inference/openai/v1":                     providers.OpencodeZenID,
		"https://opencode.ai/inference/openai/v1/chat/completions":    providers.OpencodeZenID,
		"https://opencode.ai/inference/openai/v1/responses":           providers.OpencodeZenID,
		"https://OpenCode.ai/inference/openai/v1/":                    providers.OpencodeZenID,
		"https://opencode.ai/inference/google/v1beta":                 providers.OpencodeZenID,
		"https://opencode.ai/inference/go/openai/v1":                  providers.OpencodeGoID,
		"https://opencode.ai/inference/go/openai/v1/chat/completions": providers.OpencodeGoID,
		"https://opencode.ai/inference/go/openai/v1/responses":        providers.OpencodeGoID,
		"https://OPENCODE.AI/Inference/Go/openai/v1":                  providers.OpencodeGoID,
	} {
		assert.Equal(t, want, modelruntime.DetectByURL(base), base)
	}
}

// The endpoint table decides the protocol per model: GPT, Grok and Muse are
// served on /responses, the rest on /chat/completions.
func TestOpencodeProtocolPerModel(t *testing.T) {
	for _, tc := range []struct {
		provider, model string
		want            api.API
	}{
		{providers.OpencodeZenID, "gpt-6.1-sol", api.APIOpenAIResponses},
		{providers.OpencodeZenID, "grok-4.7", api.APIOpenAIResponses},
		{providers.OpencodeZenID, "muse-spark-1.3", api.APIOpenAIResponses},
		{providers.OpencodeZenID, "qwen3.8-max", api.APIOpenAICompletions},
		{providers.OpencodeZenID, "kimi-k3", api.APIOpenAICompletions},
		{providers.OpencodeGoID, "kimi-k3", api.APIOpenAICompletions},
		{providers.OpencodeGoID, "glm-5.3", api.APIOpenAICompletions},
		{providers.OpencodeGoID, "gpt-6-luna", api.APIOpenAIResponses},
		{providers.OpencodeGoID, "muse-spark-1.3-contributor", api.APIOpenAIResponses},
	} {
		r, err := modelruntime.Resolve(modelruntime.Ref{Provider: tc.provider, Model: tc.model})
		require.NoError(t, err, tc.model)
		assert.True(t, r.Cataloged, "%s/%s", tc.provider, tc.model)
		assert.Equal(t, tc.want, r.API, "%s/%s", tc.provider, tc.model)
		def, _ := modelruntime.Get(tc.provider)
		assert.Equal(t, def.GetDefaultURL(types.ModelTypeKnowledgeQA), r.BaseURL)
	}

	// A model id the catalog does not know keeps the vendor default, so a
	// newly published chat model works without a catalog update.
	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: providers.OpencodeGoID, Model: "some-new-model"})
	require.NoError(t, err)
	assert.False(t, r.Cataloged)
	assert.Equal(t, api.APIOpenAICompletions, r.API)
}

// Models documented only on the Anthropic / Google routes, Zen's free tier and
// deprecated ids must not be offered by these presets.
func TestOpencodeCatalogScope(t *testing.T) {
	excluded := map[string][]string{
		providers.OpencodeZenID: {
			"claude-opus-5-5", "gemini-4-argon", "qwen3.8-flash", "qwen3.6-plus", "qwen3.5-plus", "jev-1.13",
			"big-pickle", "glm-5.3-flash-free", "gpt-5-codex", "kimi-k2.5",
		},
		providers.OpencodeGoID: {"claude-haiku-5-5", "gemini-4-argon", "minimax-m3", "qwen3.8-max", "beam-free"},
	}
	count := map[string]int{}
	for id, names := range excluded {
		v, ok := modelruntime.Get(id)
		require.True(t, ok)
		for _, m := range v.Models() {
			assert.NotContains(t, names, m.ID, "%s offers %s", id, m.ID)
			assert.False(t, strings.HasSuffix(m.ID, "-free"), "%s offers free-tier %s", id, m.ID)

			// Every entry, not just the samples above, follows the endpoint
			// table: GPT / Grok / Muse on /responses, the rest on
			// /chat/completions.
			want := api.APIOpenAICompletions
			for _, prefix := range []string{"gpt-", "grok-", "muse-"} {
				if strings.HasPrefix(m.ID, prefix) {
					want = api.APIOpenAIResponses
				}
			}
			r, err := modelruntime.Resolve(modelruntime.Ref{Provider: id, Model: m.ID})
			require.NoError(t, err, m.ID)
			assert.Equal(t, want, r.API, "%s/%s", id, m.ID)
			if want == api.APIOpenAICompletions {
				assert.Equal(t, "max_tokens", r.OpenAICompletions.MaxTokensField, "%s/%s", id, m.ID)
			}
			count[id]++
		}
	}
	// The endpoint tables, minus free-tier, deprecated and non-OpenAI routes.
	assert.Equal(t, map[string]int{providers.OpencodeZenID: 39, providers.OpencodeGoID: 22}, count)
}
