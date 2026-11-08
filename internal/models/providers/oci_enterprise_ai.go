// Package providers registers OCI Enterprise AI: the OpenAI-compatible
// endpoints of OCI Generative AI, scoped to a Generative AI project.
//
// Facts (checked against the Oracle docs and live endpoints on 2026-11-08):
//   - the base URL is https://inference.generativeai.<region>.oci.oraclecloud.com/openai/v1
//     and serves the Responses and Chat Completions APIs
//     (https://docs.oracle.com/en-us/iaas/Content/generative-ai/openai-compatible-api.htm);
//   - authentication is an OCI Generative AI API key sent as a Bearer token;
//     the API key and the project must belong to the endpoint's region;
//   - the project OCID (ocid1.generativeaiproject.oc1.<region>....) is sent in
//     the `OpenAI-Project` header, exactly as the OpenAI SDK's `project=`
//     argument does (https://docs.oracle.com/en-us/iaas/Content/generative-ai/responses-api.htm).
//     Without it non-OpenAI models (xAI, Gemini) answer 400 invalid_value;
//   - the models offered for agentic inference are listed on
//     https://docs.oracle.com/en-us/iaas/Content/generative-ai/agentic-regions.htm;
//     availability differs per region (xAI only in the US regions).
//
// It is configured like the OpenAI provider (Bearer key, Responses API for
// chat, images as input_image parts); the only addition is the required
// project id.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/oci_enterprise_ai.svg
var ociEnterpriseAIIcon []byte

// OCIEnterpriseAIID is the provider identifier stored on model rows.
const OCIEnterpriseAIID = "oci_enterprise_ai"

// OCIEnterpriseAIBaseURL is the default endpoint (US Midwest, Chicago: the
// region with the widest model list).
const OCIEnterpriseAIBaseURL = "https://inference.generativeai.us-chicago-1.oci.oraclecloud.com/openai/v1"

// OCIEnterpriseAIProjectKey is the extra_config key holding the project OCID.
const OCIEnterpriseAIProjectKey = "project_id"

func newOCIEnterpriseAIProvider() *Definition {
	return &Definition{
		ID:    OCIEnterpriseAIID,
		Name:  "OCI Enterprise AI",
		Names: map[string]string{"zh-CN": "OCI Enterprise AI"},
		Description: "Oracle OCI Generative AI OpenAI-compatible endpoint with a project: " +
			"xai.grok-4.3, google.gemini-2.5-flash, openai.gpt-oss-120b, etc.",
		Descriptions: map[string]string{
			"zh-CN": "Oracle OCI Generative AI 的 OpenAI 兼容端点（需项目 OCID）：" +
				"xai.grok-4.3、google.gemini-2.5-flash、openai.gpt-oss-120b 等",
		},
		Website:      "https://docs.oracle.com/en-us/iaas/Content/generative-ai/openai-compatible-api.htm",
		Icon:         ociEnterpriseAIIcon,
		API:          api.APIOpenAICompletions,
		Order:        54,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"oci.oraclecloud.com/openai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OCIEnterpriseAIBaseURL,
			types.ModelTypeVLLM:        OCIEnterpriseAIBaseURL,
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeVLLM},
		ExtraFields: []ExtraField{{
			Key:         OCIEnterpriseAIProjectKey,
			Label:       "Project OCID",
			Labels:      map[string]string{"zh-CN": "项目 OCID"},
			Type:        "string",
			Required:    true,
			Placeholder: "ocid1.generativeaiproject.oc1.<region>.…",
			Header:      "OpenAI-Project",
		}},
		PreferAPI: ociEnterpriseAIPreferResponses,
	}
}

// ociEnterpriseAIPreferResponses applies the OpenAI provider's rule on every
// OCI host: chat traffic uses the Responses API (VLM requests go through the
// same chat client, so they use it too).
func ociEnterpriseAIPreferResponses(_ string, spec models.ModelSpec) api.API {
	if spec.Type != "" && spec.Type != types.ModelTypeKnowledgeQA {
		return ""
	}
	return api.APIOpenAIResponses
}
