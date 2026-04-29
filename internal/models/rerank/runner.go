package rerank

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/httpx"
)

// httpReranker is the single concrete type backing every HTTP-transport
// reranker (openai / aliyun / jina / zhipu / nvidia). Provider-specific
// behavior lives in rerankerSpec; everything else (HTTP call, custom headers,
// logging) is shared.
type httpReranker struct {
	spec          rerankerSpec
	apiKey        string
	baseURL       string
	modelName     string
	modelID       string
	httpClient    *http.Client
	customHeaders map[string]string
}

// rerankerSpec captures the three things that differ between providers:
// endpoint URL, request body shape, response parsing.
type rerankerSpec struct {
	providerName   string
	defaultBaseURL string

	// fullEndpointURL, when true, treats the configured BaseURL as the full
	// rerank endpoint URL (Aliyun, Zhipu, Nvidia). When false, "/rerank" is
	// appended (OpenAI, Jina).
	fullEndpointURL bool

	// buildBody marshals the request for the given query + documents.
	buildBody func(r *httpReranker, query string, documents []string) ([]byte, error)

	// parseBody extracts []RankResult from the response body. Implementations
	// may consult the original documents slice to fill in Document.Text when
	// the provider doesn't echo it back.
	parseBody func(body []byte, documents []string) ([]RankResult, error)
}

func newHTTPReranker(config *RerankerConfig, spec rerankerSpec) (*httpReranker, error) {
	if config == nil {
		return nil, fmt.Errorf("reranker config is nil")
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = spec.defaultBaseURL
	}
	return &httpReranker{
		spec:          spec,
		apiKey:        config.APIKey,
		baseURL:       baseURL,
		modelName:     config.ModelName,
		modelID:       config.ModelID,
		httpClient:    &http.Client{Timeout: 60 * time.Second},
		customHeaders: config.CustomHeaders,
	}, nil
}

// SetCustomHeaders is kept so the factory in reranker.go can inject headers
// via the customHeaderSetter interface after construction.
func (r *httpReranker) SetCustomHeaders(headers map[string]string) {
	r.customHeaders = headers
}

// Rerank performs the HTTP call and delegates body building / response parsing
// to the spec.
func (r *httpReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	body, err := r.spec.buildBody(r, query, documents)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	endpoint := r.baseURL
	if !r.spec.fullEndpointURL {
		endpoint = r.baseURL + "/rerank"
	}

	logger.Debugf(ctx, "%s", buildRerankRequestDebug(r.modelName, endpoint, query, documents))

	resp, err := httpx.DoPOST(ctx, httpx.POSTRequest{
		URL:     endpoint,
		Body:    body,
		Headers: map[string]string{"Authorization": "Bearer " + r.apiKey},
		// Rerankers historically did no retry; preserve that so SLOs don't
		// suddenly change for callers expecting fast-fail behavior.
		MaxRetries:    0,
		HTTPClient:    r.httpClient,
		CustomHeaders: r.customHeaders,
		LogPrefix:     r.spec.providerName + "Reranker",
	})
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Some providers include the body in the error for debugging (Aliyun,
		// Zhipu, Jina). OpenAI historically didn't. Include it uniformly now.
		return nil, fmt.Errorf("%s rerank API error: Http Status: %s, Body: %s",
			r.spec.providerName, resp.Status, string(respBody))
	}

	return r.spec.parseBody(respBody, documents)
}

func (r *httpReranker) GetModelName() string { return r.modelName }
func (r *httpReranker) GetModelID() string   { return r.modelID }
