// Package providers registers the WeKnora managed model service.
//
// unverified: this is a first-party endpoint with no published API
// reference — https://weknora.weixin.qq.com/docs documents the WeKnora
// product, not the model gateway's wire protocol. Every fact below is taken
// from this repository's own clients rather than from vendor documentation,
// and is cited accordingly:
//   - one host serves chat, embedding, rerank and VLM. Chat completions and
//     VLM live at /api/v1/chat/completions rather than the OpenAI default
//     path (internal/models/vlm/weknoracloud.go), embeddings at
//     /api/v1/embeddings (internal/models/embedding/weknoracloud.go) and
//     rerank at /api/v1/rerank (internal/models/rerank/weknoracloud.go).
//     Only the chat path goes through the Endpoint hook; the other three
//     clients build their own URLs;
//   - requests are signed with the tenant's AppID / AppSecret through
//     modelutils.Sign (internal/models/utils/signer.go), which sends
//     X-APPID, X-API-Key, X-Request-ID, X-Timestamp, X-Nonce and an
//     X-Signature that is the MD5 of the RFC3986-encoded, key-sorted
//     parameter string including the MD5 of the body. There is no bearer
//     key, hence the AuthSigned style and a fresh request id per call;
//   - credentials are provisioned through the dedicated initialization
//     endpoint (internal/handler/weknoracloud.go), so Validate performs no
//     key check here;
//   - the model names the product provisions are chat, embedding, rerank and
//     vlm (frontend/src/utils/weknoraCloudModels.ts). models.json is left
//     empty on purpose: no context window, output cap or price is published
//     for them, and the embedding dimension is probed at runtime through
//     GET /api/v1/models/weknoracloud/status.
//
// unverified: SupportsMultiContent is kept false so chat messages are
// flattened to plain text, but nothing documents that restriction and the
// VLM client posts image_url parts to the very same /api/v1/chat/completions
// path, so it may only apply to the chat model. The value is left as-is
// because the transport test pins it.
//
// unverified: PromptCacheAccounting is kept false; the gateway is not
// documented to report prompt-cache counters, and none have been observed.
package providers

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

//go:embed assets/weknoracloud.svg
var weKnoraCloudIcon []byte

// WeKnoraCloudID is the provider identifier stored on model rows.
const WeKnoraCloudID = "weknoracloud"

// WeKnoraCloudBaseURL is the single service entry point; per-capability paths are
// appended by the Endpoint hook and the embedding / rerank clients.
const WeKnoraCloudBaseURL = "https://weknora.weixin.qq.com"

func newWeKnoraCloudProvider() *Definition {
	return &Definition{
		ID:           WeKnoraCloudID,
		Name:         "WeKnora Cloud",
		Names:        map[string]string{"zh-CN": "WeKnora 云服务"},
		Description:  "WeKnora managed models: chat, embedding, rerank, vlm",
		Descriptions: map[string]string{"zh-CN": "WeKnora云服务，模型：chat, embedding, rerank, vlm"},
		Website:      WeKnoraCloudBaseURL,
		Icon:         weKnoraCloudIcon,
		API:          api.APIOpenAICompletions,
		Order:        1,
		RequiresAuth: true,
		Auth:         AuthSigned,
		URLPatterns:  []string{"weknora.weixin.qq.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: WeKnoraCloudBaseURL,
			types.ModelTypeEmbedding:   WeKnoraCloudBaseURL,
			types.ModelTypeRerank:      WeKnoraCloudBaseURL,
			types.ModelTypeVLLM:        WeKnoraCloudBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				// The OpenAI body on the service's own path, signed like the
				// rest of it.
				Path:            api.Ptr("/api/v1/embeddings"),
				DimensionsField: api.Ptr("dimensions"),
				RequestTimeout:  api.Ptr(60),
			},
			Rerank: api.RerankCompat{
				Path: api.Ptr("/api/v1/rerank"),
				// The only rerank vendor that has ever had a client deadline
				// here. Without it a hung endpoint holds the retrieval stage
				// for as long as the caller's context allows.
				RequestTimeout: api.Ptr(60),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				SupportsMultiContent:  api.Ptr(false),
				PromptCacheAccounting: api.Ptr(false),
			},
		},
		Signer: func(creds api.Credentials) api.AuthFunc {
			return func(req *http.Request, body []byte) {
				headers := modelutils.Sign(creds.AppID, creds.AppSecret, uuid.NewString(), string(body))
				for k, v := range headers {
					req.Header.Set(k, v)
				}
			}
		},
		Endpoint: func(r EndpointRequest) (string, map[string]string) {
			switch r.ModelType {
			case types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeASR:
				// Those clients build their own paths; fall back.
				return "", nil
			}
			return strings.TrimRight(r.BaseURL, "/") + "/api/v1/chat/completions", nil
		},
		// AppID / AppSecret are written by the dedicated initialization
		// endpoint; only structural checks happen there.
		Validate: func(_ *Config) error { return nil },
	}
}
