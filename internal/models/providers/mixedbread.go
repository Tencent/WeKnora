// Package providers registers Mixedbread's hosted rerank endpoint.
// https://mixedbread.com/docs
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/mixedbread.svg
var mixedbreadIcon []byte

// MixedbreadID identifies the Mixedbread provider in the model catalog.
const MixedbreadID = "mixedbread"

// MixedbreadRerankBaseURL is the default Mixedbread API endpoint.
const MixedbreadRerankBaseURL = "https://api.mixedbread.com"

// The catalog IDs follow the open-source mxbai-rerank v2 family listed under
// the mixedbread-ai organization on Hugging Face:
// https://huggingface.co/mixedbread-ai (mxbai-rerank-base-v2,
// mxbai-rerank-large-v2).
func newMixedbreadProvider() *Definition {
	return &Definition{
		ID: MixedbreadID, Name: "Mixedbread",
		Description: "Mixedbread standalone reranking (mxbai-rerank-base-v2, mxbai-rerank-large-v2)",
		Website:     "https://mixedbread.com", Icon: mixedbreadIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankMixedbread,
		Order: 57, RequiresAuth: true, Auth: AuthBearer,
		URLPatterns:     []string{"api.mixedbread.com"},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: MixedbreadRerankBaseURL},
		ModelTypes:      []types.ModelType{types.ModelTypeRerank},
	}
}
