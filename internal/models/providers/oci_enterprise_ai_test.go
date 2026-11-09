package providers_test

import (
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOCIEnterpriseAIProvider(t *testing.T) {
	v, ok := modelruntime.Get(providers.OCIEnterpriseAIID)
	require.True(t, ok)
	for _, mt := range []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeVLLM} {
		assert.Equal(t, providers.OCIEnterpriseAIBaseURL, v.GetDefaultURL(mt), mt)
	}
	assert.Equal(t, providers.OCIEnterpriseAIID,
		modelruntime.DetectByURL("https://inference.generativeai.ap-osaka-1.oci.oraclecloud.com/openai/v1"))

	// Chat goes through the Responses API, like the OpenAI provider.
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: providers.OCIEnterpriseAIID, Model: "xai.grok-4.7", ModelType: types.ModelTypeKnowledgeQA,
	})
	require.NoError(t, err)
	assert.True(t, r.Cataloged)
	assert.Equal(t, api.APIOpenAIResponses, r.API)

	// The project id is required on save and travels as the OpenAI-Project header.
	params := &types.ModelParameters{Provider: providers.OCIEnterpriseAIID}
	assert.ErrorContains(t, modelruntime.ValidateRow("xai.grok-4.7", types.ModelTypeKnowledgeQA, params), "project_id")
	params.ExtraConfig = map[string]string{providers.OCIEnterpriseAIProjectKey: "ocid1.generativeaiproject.oc1.x"}
	assert.NoError(t, modelruntime.ValidateRow("xai.grok-4.7", types.ModelTypeKnowledgeQA, params))

	ep, err := r.Endpoint(types.ModelTypeKnowledgeQA, modelruntime.Connection{
		Credentials: api.Credentials{APIKey: "k"},
		Extra:       params.ExtraConfig,
		Headers:     map[string]string{"X-Other": "1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ocid1.generativeaiproject.oc1.x", ep.Headers["OpenAI-Project"])
	assert.Equal(t, "1", ep.Headers["X-Other"])

	// An explicit custom header of the same name wins over the field.
	ep, err = r.Endpoint(types.ModelTypeKnowledgeQA, modelruntime.Connection{
		Extra:   params.ExtraConfig,
		Headers: map[string]string{"OpenAI-Project": "override"},
	})
	require.NoError(t, err)
	assert.Equal(t, "override", ep.Headers["OpenAI-Project"])

	// An empty project sends no header at all.
	ep, err = r.Endpoint(types.ModelTypeKnowledgeQA, modelruntime.Connection{})
	require.NoError(t, err)
	assert.NotContains(t, ep.Headers, "OpenAI-Project")

	// A custom header wins whatever its case, and only one is sent.
	ep, err = r.Endpoint(types.ModelTypeKnowledgeQA, modelruntime.Connection{
		Extra:   params.ExtraConfig,
		Headers: map[string]string{"openai-project": "lower"},
	})
	require.NoError(t, err)
	assert.Equal(t, "lower", ep.Headers["openai-project"])
	assert.NotContains(t, ep.Headers, "OpenAI-Project")

	// Rows that passed the project as a custom header before the field
	// existed stay valid.
	legacy := &types.ModelParameters{
		Provider:      providers.OCIEnterpriseAIID,
		CustomHeaders: map[string]string{"OpenAI-Project": "ocid1.generativeaiproject.oc1.x"},
	}
	assert.NoError(t, modelruntime.ValidateRow("xai.grok-4.7", types.ModelTypeKnowledgeQA, legacy))
}

// Thinking levels follow what the OCI Responses endpoint accepts (checked
// live): Grok 4.7/4.3 take minimal..high but cannot stop reasoning and
// reject xhigh; gpt-oss rejects minimal; Grok 4.20 reasoning and Gemini 2.5
// reject reasoning.effort entirely, so only automatic thinking is offered.
func TestOCIEnterpriseAIThinkingLevels(t *testing.T) {
	levels := func(model string) api.ThinkingLevelMap {
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: providers.OCIEnterpriseAIID, Model: model, ModelType: types.ModelTypeKnowledgeQA,
		})
		require.NoError(t, err)
		return r.ThinkingLevels
	}
	for _, model := range []string{"xai.grok-4.7", "xai.grok-4.3"} {
		l := levels(model)
		assert.False(t, l.Supports(api.ReasoningOff), model)
		assert.True(t, l.Supports(api.ReasoningMinimal), model)
		assert.True(t, l.Supports(api.ReasoningHigh), model)
		assert.False(t, l.Supports(api.ReasoningXHigh), model)
	}
	for _, model := range []string{"openai.gpt-oss-120b", "openai.gpt-oss-20b"} {
		l := levels(model)
		assert.False(t, l.Supports(api.ReasoningOff), model)
		assert.False(t, l.Supports(api.ReasoningMinimal), model)
		assert.True(t, l.Supports(api.ReasoningLow), model)
	}
	for _, model := range []string{"xai.grok-4.20-reasoning", "google.gemini-2.5-pro", "google.gemini-2.5-flash"} {
		assert.Equal(t, []api.ReasoningEffort{api.ReasoningAuto}, levels(model).SupportedLevels(), model)
		// A requested level never reaches the wire as an effort.
		assert.Equal(t, api.ReasoningAuto, levels(model).Clamp(api.ReasoningHigh), model)
	}
}

// The VLM picker only offers models that accept images: those whose Oracle
// model page states it, plus Grok 4.7 (verified on the live endpoint).
// Text-only (gpt-oss) and undocumented, unverified (grok-4.6) models stay
// chat-only.
func TestOCIEnterpriseAIVisionModels(t *testing.T) {
	v, ok := modelruntime.Get(providers.OCIEnterpriseAIID)
	require.True(t, ok)
	ids := func(mt types.ModelType) []string {
		var out []string
		for _, m := range v.ModelsByType(mt) {
			out = append(out, m.ID)
		}
		sort.Strings(out)
		return out
	}
	assert.Equal(t, []string{
		"google.gemini-2.5-flash", "google.gemini-2.5-flash-lite", "google.gemini-2.5-pro",
		"openai.gpt-oss-120b", "openai.gpt-oss-20b",
		"xai.grok-4.20-non-reasoning", "xai.grok-4.20-reasoning", "xai.grok-4.3", "xai.grok-4.6", "xai.grok-4.7",
	}, ids(types.ModelTypeKnowledgeQA))
	assert.Equal(t, []string{
		"google.gemini-2.5-flash", "google.gemini-2.5-flash-lite", "google.gemini-2.5-pro",
		"xai.grok-4.20-non-reasoning", "xai.grok-4.20-reasoning", "xai.grok-4.3", "xai.grok-4.7",
	}, ids(types.ModelTypeVLLM))
}
