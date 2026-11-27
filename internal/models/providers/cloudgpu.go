// Package providers registers CloudGPU's OpenAI-compatible chat API.
//
// Facts (https://cloudgpu.app/api/docs, and GET https://cloudgpu.app/v1/models
// on 2026-11-02, which needs no key):
//   - the only base URL is https://cloudgpu.app/v1. Chat completions are
//     POST /v1/chat/completions and the docs describe that body as the
//     OpenAI chat-completions shape, so the output cap is
//     max_completion_tokens;
//   - auth is Authorization: Bearer <API Key>. Keys are created in the
//     dashboard at https://cloudgpu.app;
//   - every id below is type "chat" with input_modalities ["text"] on that
//     public list. The docs say embeddings are not available, and none of
//     these ids advertise image input, so this package registers chat only;
//   - context windows are the context_length values from that response.
//     Prices are left unset.
//
// unverified: the docs do not name a thinking switch, so reasoning_effort
// is not sent and the catalog does not claim a reasoning ladder.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/cloudgpu.svg
var cloudgpuIcon []byte

// CloudGPUID is the provider identifier stored on model rows.
const CloudGPUID = "cloudgpu"

// CloudGPUBaseURL is the OpenAI-compatible chat endpoint.
const CloudGPUBaseURL = "https://cloudgpu.app/v1"

func newCloudGPUProvider() *Definition {
	return &Definition{
		ID:    CloudGPUID,
		Name:  "CloudGPU",
		Names: map[string]string{"zh-CN": "CloudGPU"},
		Description: "deepseek-v4-pro, deepseek-v4-flash, kimi-k3, glm-5.2, " +
			"minimax-m3, etc.",
		Website: "https://cloudgpu.app/?utm_source=weknora&utm_medium=integration" +
			"&utm_campaign=202611_seg06_indiedev_api",
		Icon:         cloudgpuIcon,
		API:          api.APIOpenAICompletions,
		Order:        54,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"cloudgpu.app"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: CloudGPUBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
		},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_completion_tokens"),
			},
		},
	}
}
