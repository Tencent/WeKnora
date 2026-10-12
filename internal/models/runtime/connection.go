package runtime

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connection contains only one configured model's credentials and transport
// settings. It is never cached in the provider registry or model catalog.
type Connection struct {
	ModelID     string
	Credentials api.Credentials
	Headers     map[string]string
	Extra       map[string]string
	Client      *http.Client
}

// Endpoint assembles authentication, headers and vendor endpoint routing for
// every capability. Protocol clients receive the result without knowing vendors.
func (r *Resolved) Endpoint(kind types.ModelType, c Connection) (api.Endpoint, error) {
	v := r.Vendor
	creds := c.Credentials
	if creds.APIKey == "" {
		creds.APIKey = v.DefaultAPIKey
	}
	protocol := v.API
	label := strings.ToLower(string(kind))
	if kind == types.ModelTypeKnowledgeQA || kind == types.ModelTypeVLLM {
		protocol, label = r.API, "provider"
	}
	if v.AuthStyleFor(protocol) == providers.AuthSigned {
		if creds.AppID == "" {
			return api.Endpoint{}, fmt.Errorf("%s %s: AppID is required", v.Name, label)
		}
		if creds.AppSecret == "" {
			return api.Endpoint{}, fmt.Errorf("%s %s: AppSecret is required", v.Name, label)
		}
	} else if v.RequiresAuth && protocol == api.APIAnthropicMessages &&
		(kind == types.ModelTypeKnowledgeQA || kind == types.ModelTypeVLLM) && strings.TrimSpace(creds.APIKey) == "" {
		return api.Endpoint{}, fmt.Errorf("%s provider: API key is required", v.Name)
	}
	headers := make(map[string]string, len(v.Headers)+len(c.Headers))
	for k, val := range v.Headers {
		headers[k] = val
	}
	for k, val := range c.Headers {
		headers[k] = val
	}
	ep := api.Endpoint{
		BaseURL: r.BaseURL, Model: r.RemoteModel, ModelID: c.ModelID,
		Auth: v.AuthFunc(protocol, creds), Headers: headers, Client: c.Client,
	}
	if v.Endpoint != nil {
		req := providers.EndpointRequest{
			BaseURL:   r.BaseURL,
			Model:     r.RemoteModel,
			ModelType: kind,
			API:       protocol,
			Extra:     c.Extra,
		}
		if kind == types.ModelTypeEmbedding {
			req.EmbeddingAPI = r.EmbeddingAPI
		}
		// Preserve historical rerank hook input; its routing uses ModelType.
		if kind == types.ModelTypeRerank {
			req.API = ""
		}
		if url, query := v.Endpoint(req); url != "" {
			ep.URL, ep.Query = url, query
		}
	}
	return ep, nil
}
