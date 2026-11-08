// Package providers registers OCI Generative AI: the native inference API
// that serves the embedding and rerank models, signed with OCI IAM.
//
// Facts (checked against the Oracle docs and live endpoints on 2026-11-08):
//   - the base URL is https://inference.generativeai.<region>.oci.oraclecloud.com;
//     embeddings are POST /20231130/actions/embedText and rerank is
//     POST /20231130/actions/rerankText, both with servingMode ON_DEMAND and a
//     compartmentId in the body
//     (https://docs.oracle.com/en-us/iaas/api/#/en/generative-ai-inference/20231130/);
//   - requests are signed with IAM-Based Authentication: a user's API signing
//     key, or the instance principal of an OCI compute instance
//     (https://docs.oracle.com/en-us/iaas/Content/generative-ai/oci-genai-auth.htm);
//   - the catalogued models are Cohere Embed 4 and Cohere Rerank 4 Fast;
//     the v3 embed and rerank 3.5 models are deprecated
//     (https://docs.oracle.com/en-us/iaas/Content/generative-ai/pretrained-models.htm).
//     Embed 4 takes up to 96 texts per request in on-demand mode.
//
// Chat on the same service is the OpenAI-compatible "OCI Enterprise AI"
// provider; this one only covers what that endpoint does not serve.
package providers

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/ocigenai"
	"github.com/Tencent/WeKnora/internal/types"
)

// OCIGenAIID is the provider identifier stored on model rows.
const OCIGenAIID = "oci_genai"

// OCIGenAIBaseURL is the default endpoint (US Midwest, Chicago), the same
// region as OCI Enterprise AI's default.
const OCIGenAIBaseURL = "https://inference.generativeai.us-chicago-1.oci.oraclecloud.com"

func newOCIGenAIProvider() *Definition {
	return &Definition{
		ID:    OCIGenAIID,
		Name:  "OCI Generative AI",
		Names: map[string]string{"zh-CN": "OCI Generative AI"},
		Description: "Oracle OCI Generative AI embedding and rerank (cohere.embed-v4.0, " +
			"cohere.rerank-v4.0-fast), signed with OCI IAM",
		Descriptions: map[string]string{
			"zh-CN": "Oracle OCI Generative AI 的向量化与重排序（cohere.embed-v4.0、" +
				"cohere.rerank-v4.0-fast），使用 OCI IAM 签名认证",
		},
		Website:      "https://docs.oracle.com/en-us/iaas/Content/generative-ai/oci-genai-auth.htm",
		Icon:         ociEnterpriseAIIcon,
		API:          api.APIOpenAICompletions,
		EmbeddingAPI: api.EmbeddingOCI,
		RerankAPI:    api.RerankOCI,
		Order:        55,
		// The signer replaces the endpoint's auth inside the protocol client,
		// where a bad key can fail the build instead of every request.
		Auth: AuthNone,
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeEmbedding: OCIGenAIBaseURL,
			types.ModelTypeRerank:    OCIGenAIBaseURL,
		},
		ModelTypes: []types.ModelType{types.ModelTypeEmbedding, types.ModelTypeRerank},
		// There is no bearer key; the generic API key input carries the
		// passphrase of an encrypted private key.
		CredentialLabels: []CredentialLabel{{
			Label:        "Private key passphrase",
			Labels:       map[string]string{"zh-CN": "私钥口令"},
			Placeholder:  "Leave empty unless the private key is encrypted",
			Placeholders: map[string]string{"zh-CN": "私钥未加密时留空"},
			Hint:         "Only for an encrypted private key. Not used with instance principal.",
			Hints:        map[string]string{"zh-CN": "仅在私钥已加密时填写；实例主体认证无需填写。"},
		}},
		ExtraFields: []ExtraField{
			{
				Key:      ocigenai.KeyAuthMode,
				Label:    "Authentication",
				Labels:   map[string]string{"zh-CN": "认证方式"},
				Type:     "select",
				Default:  ocigenai.AuthAPIKey,
				Required: true,
				Options: []ExtraFieldOption{
					{
						Label: "API signing key", Labels: map[string]string{"zh-CN": "API 签名密钥"},
						Value: ocigenai.AuthAPIKey,
					},
					{
						Label: "Instance principal", Labels: map[string]string{"zh-CN": "实例主体"},
						Value: ocigenai.AuthInstancePrincipal,
					},
				},
			},
			{
				Key:         ocigenai.KeyCompartment,
				Label:       "Compartment OCID",
				Labels:      map[string]string{"zh-CN": "区间 OCID"},
				Type:        "string",
				Required:    true,
				Placeholder: "ocid1.compartment.oc1..…",
			},
			{
				Key:          ocigenai.KeyTenancy,
				Label:        "Tenancy OCID",
				Labels:       map[string]string{"zh-CN": "租户 OCID"},
				Type:         "string",
				Placeholder:  "ocid1.tenancy.oc1..… (API signing key only)",
				Placeholders: map[string]string{"zh-CN": "ocid1.tenancy.oc1..…（仅 API 签名密钥）"},
			},
			{
				Key:          ocigenai.KeyUser,
				Label:        "User OCID",
				Labels:       map[string]string{"zh-CN": "用户 OCID"},
				Type:         "string",
				Placeholder:  "ocid1.user.oc1..… (API signing key only)",
				Placeholders: map[string]string{"zh-CN": "ocid1.user.oc1..…（仅 API 签名密钥）"},
			},
			{
				Key:          ocigenai.KeyFingerprint,
				Label:        "Fingerprint",
				Labels:       map[string]string{"zh-CN": "指纹"},
				Type:         "string",
				Placeholder:  "12:34:56:… (API signing key only)",
				Placeholders: map[string]string{"zh-CN": "12:34:56:…（仅 API 签名密钥）"},
			},
			{
				// Stored as the row's encrypted app_secret credential.
				Key:          "private_key",
				Label:        "API signing private key",
				Labels:       map[string]string{"zh-CN": "API 签名私钥"},
				Type:         "file",
				Accept:       ".pem",
				Secret:       true,
				Placeholder:  "Upload the .pem private key (API signing key only)",
				Placeholders: map[string]string{"zh-CN": "上传 .pem 私钥文件（仅 API 签名密钥）"},
			},
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				// Embed 4, on-demand: at most 96 texts per request.
				MaxBatchSize: api.Ptr(96),
			},
		},
	}
}
