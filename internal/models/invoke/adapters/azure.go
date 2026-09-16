package adapters

// azure.go — the azure_openai vendor deltas. The chat funnel's azure branches
// (api-key auth, deployment URL + api-version query) are woven into the
// openai family plumbing via the spec.azure flag (openai.go) — extracting
// them here means giving azure its own composite and is deferred until the
// family funnel is next touched. This file holds the facet wire that is
// purely azure's: the deployment-path embedding builder.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// buildAzureEmbedding ports v1 azure_openai.go: deployment-path URL,
// api-version query (Endpoint.APIVersion, default 2024-10-21), api-key header.
func buildAzureEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if ep.BaseURL == "" {
		return nil, fmt.Errorf("azure resource endpoint (base URL) is required")
	}
	if model == "" {
		return nil, fmt.Errorf("deployment name (model name) is required")
	}
	apiVersion := ep.APIVersion
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}
	body := openAIEmbedRequest{
		Model:          model,
		Input:          opts.Inputs,
		EncodingFormat: "float",
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		body.Dimensions = opts.Dimensions
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Api-Key", ep.Credentials.APIKey)
	return &invoke.Request{
		Method: http.MethodPost,
		URL: fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
			ep.BaseURL, model, apiVersion),
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}
