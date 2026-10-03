// Package providers registers Amazon Bedrock Agent Runtime's Rerank action.
// https://docs.aws.amazon.com/bedrock/latest/userguide/rerank-use.html
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/bedrock.svg
var bedrockIcon []byte

// BedrockID identifies the Bedrock provider in the model catalog.
const BedrockID = "bedrock"

// BedrockRerankBaseURL is the default Agent Runtime endpoint.
const BedrockRerankBaseURL = "https://bedrock-agent-runtime.us-west-2.amazonaws.com"

func newBedrockProvider() *Definition {
	rerankOnly := []types.ModelType{types.ModelTypeRerank}
	return &Definition{
		ID: BedrockID, Name: "Amazon Bedrock",
		Names:       map[string]string{"zh-CN": "Amazon Bedrock"},
		Description: "Amazon Rerank 1.0 and Cohere Rerank 3.5 through Bedrock Agent Runtime",
		Website:     "https://aws.amazon.com/bedrock/", Icon: bedrockIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankBedrock,
		Order: 55, RequiresAuth: true,
		// The AWS SDK signs the rerank request with the row's AK/SK pair.
		// No bearer header should be built by the generic endpoint layer.
		Auth:            AuthNone,
		URLPatterns:     []string{"bedrock-agent-runtime."},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: BedrockRerankBaseURL},
		ModelTypes:      rerankOnly,
		CredentialLabels: []CredentialLabel{{
			Label: "AWS Access Key ID", Labels: map[string]string{"zh-CN": "AWS Access Key ID"},
			Placeholder: "AKIA...",
			Hint:        "Use a static AWS access-key pair; temporary session tokens are not supported.",
			Hints: map[string]string{
				"zh-CN": "使用静态 AWS AK/SK 密钥对；暂不支持临时凭证的 Session Token。",
			},
			ModelTypes: rerankOnly, Required: true,
		}},
		ExtraFields: []ExtraField{
			{
				Key: "secret_key", Label: "AWS Secret Access Key",
				Labels: map[string]string{"zh-CN": "AWS Secret Access Key"},
				Type:   "password", Required: true, Secret: true, ModelTypes: rerankOnly,
				Placeholder:  "AWS Secret Access Key",
				Placeholders: map[string]string{"zh-CN": "AWS 私密访问密钥"},
			},
			{
				Key: "region", Label: "AWS Region", Labels: map[string]string{"zh-CN": "AWS 地域"},
				Type: "select", Default: "us-west-2", ModelTypes: rerankOnly,
				Options: []ExtraFieldOption{
					{Label: "us-west-2", Value: "us-west-2"},
					{
						Label:  "us-east-1 (Cohere only)",
						Labels: map[string]string{"zh-CN": "us-east-1（仅 Cohere）"},
						Value:  "us-east-1",
					},
					{Label: "ap-northeast-1", Value: "ap-northeast-1"},
					{Label: "ca-central-1", Value: "ca-central-1"},
					{Label: "eu-central-1", Value: "eu-central-1"},
				},
			},
		},
		Compat: VendorCompat{Rerank: api.RerankCompat{
			// Rerank accepts one query, 1-1000 sources and text up to 32000
			// characters per query/source.
			// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_agent-runtime_Rerank.html
			MaxDocuments: api.Ptr(1000), MaxQueryChars: api.Ptr(32000),
			MaxDocumentChars: api.Ptr(32000),
		}},
	}
}
