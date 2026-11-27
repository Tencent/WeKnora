// Package providers registers IBM watsonx.ai's native text rerank endpoint.
// https://cloud.ibm.com/apidocs/watsonx-ai#text-rerank
package providers

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// WatsonxID identifies the IBM watsonx.ai provider in the model catalog.
const WatsonxID = "watsonx"

// WatsonxRerankBaseURL is the default watsonx.ai service endpoint
// (us-south). Other regions use the same path under their own host.
const WatsonxRerankBaseURL = "https://us-south.ml.cloud.ibm.com"

func newWatsonxProvider() *Definition {
	return &Definition{
		ID: WatsonxID, Name: "IBM watsonx.ai",
		Names:       map[string]string{"zh-CN": "IBM watsonx.ai"},
		Description: "IBM watsonx.ai native text rerank (cross-encoder/ms-marco-minilm-l-12-v2)",
		Descriptions: map[string]string{
			"zh-CN": "IBM watsonx.ai 原生文本重排（cross-encoder/ms-marco-minilm-l-12-v2）",
		},
		Website: "https://www.ibm.com/watsonx", Icon: genericIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankWatsonx,
		Order: 62, RequiresAuth: true, Auth: AuthBearer,
		URLPatterns:     []string{"ml.cloud.ibm.com"},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: WatsonxRerankBaseURL},
		ModelTypes:      []types.ModelType{types.ModelTypeRerank},
		Validate: func(cfg *Config) error {
			if cfg == nil {
				return fmt.Errorf("config is nil")
			}
			if strings.TrimSpace(cfg.BaseURL) == "" {
				return fmt.Errorf("base URL is required for IBM watsonx.ai provider")
			}
			if strings.TrimSpace(cfg.ModelName) == "" {
				return fmt.Errorf("model name is required")
			}
			return nil
		},
	}
}
