package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/httpx"
)

// httpEmbedder is the single concrete type backing every HTTP-transport
// embedder (openai / jina / nvidia / azure_openai / volcengine / aliyun).
// Previously each provider had its own struct of ~20 fields with identical
// methods and a near-identical doRequestWithRetry body.
//
// All per-provider behavior lives in the embedderSpec supplied at construction.
type httpEmbedder struct {
	spec                 embedderSpec
	apiKey               string
	baseURL              string
	modelName            string
	modelID              string
	apiVersion           string // Azure-only, left empty for others
	dimensions           int
	truncatePromptTokens int
	httpClient           *http.Client
	maxRetries           int
	customHeaders        map[string]string
	EmbedderPooler
}

// embedderSpec captures the three things that differ between providers: the
// endpoint URL, the request body, and the response parser. Everything else
// (retry, headers, HTTP client setup) is shared.
type embedderSpec struct {
	// providerName is used as the retry log prefix, e.g. "OpenAIEmbedder".
	providerName string

	// defaultBaseURL is used when the caller supplies no BaseURL. If the
	// provider requires a BaseURL (e.g. Azure), set this to "" and let
	// newHTTPEmbedder's validate hook reject empty BaseURL.
	defaultBaseURL string

	// normalizeBaseURL runs once at construction after applying defaultBaseURL.
	// Providers like Aliyun strip "/compatible-mode/v1" suffixes. Optional.
	normalizeBaseURL func(baseURL string) string

	// validate runs once at construction to reject missing required config
	// (e.g. empty BaseURL for Azure). Optional.
	validate func(e *httpEmbedder) error

	// buildURL returns the full endpoint for a BatchEmbed call. This is a
	// function so Azure can interpolate deployment name + api-version.
	buildURL func(e *httpEmbedder) string

	// buildAuthHeaders returns headers applied before custom headers. Default
	// is Bearer <apiKey>; Azure uses api-key instead.
	buildAuthHeaders func(e *httpEmbedder) map[string]string

	// batchMode decides how a BatchEmbed of N texts translates to HTTP calls.
	// Most providers do one call with N inputs (batchAll). Volcengine's
	// multimodal endpoint returns one embedding per call, so it uses batchOne.
	batchMode batchMode

	// buildBody marshals the request for the given batch of texts. Called
	// once per HTTP call (so once per Embed for batchOne, once per BatchEmbed
	// for batchAll).
	buildBody func(ctx context.Context, e *httpEmbedder, texts []string) ([]byte, error)

	// parseBody extracts [][]float32 from the response body. For batchOne,
	// the slice will have exactly one element.
	parseBody func(body []byte, texts []string) ([][]float32, error)

	// formatError lets providers enrich error messages with structured API
	// error codes (e.g. Aliyun "InvalidApiKey - ..."). Optional; default is
	// "API error: Http Status %s".
	formatError func(body []byte, status string) error
}

type batchMode int

const (
	batchAll batchMode = iota // one HTTP call for all texts
	batchOne                  // one HTTP call per text
)

// newHTTPEmbedder is the common constructor used by every spec. It applies
// defaults, runs the spec's validate hook, and returns the ready-to-use impl.
func newHTTPEmbedder(config Config, pooler EmbedderPooler, spec embedderSpec) (*httpEmbedder, error) {
	if config.ModelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = spec.defaultBaseURL
	}
	if spec.normalizeBaseURL != nil {
		baseURL = spec.normalizeBaseURL(baseURL)
	}
	truncate := config.TruncatePromptTokens
	if truncate == 0 {
		truncate = 511
	}

	e := &httpEmbedder{
		spec:                 spec,
		apiKey:               config.APIKey,
		baseURL:              baseURL,
		modelName:            config.ModelName,
		modelID:              config.ModelID,
		dimensions:           config.Dimensions,
		truncatePromptTokens: truncate,
		httpClient:           &http.Client{Timeout: 60 * time.Second},
		maxRetries:           3,
		EmbedderPooler:       pooler,
		customHeaders:        config.CustomHeaders,
	}
	if config.ExtraConfig != nil {
		// Azure uses ExtraConfig["api_version"].
		if v, ok := config.ExtraConfig["api_version"]; ok {
			e.apiVersion = v
		}
	}
	if spec.validate != nil {
		if err := spec.validate(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// SetCustomHeaders is kept for compatibility — it was called from the old
// factory funcs. New code sets headers via Config.CustomHeaders, but this
// method is still exercised by embedder.go before the refactor is fully done.
func (e *httpEmbedder) SetCustomHeaders(headers map[string]string) {
	e.customHeaders = headers
}

// Embed embeds a single text by calling BatchEmbed once; the retry-on-empty
// behavior that every provider used to copy (3 attempts) is preserved.
func (e *httpEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	for range 3 {
		embeddings, err := e.BatchEmbed(ctx, []string{text})
		if err != nil {
			return nil, err
		}
		if len(embeddings) > 0 {
			return embeddings[0], nil
		}
	}
	return nil, fmt.Errorf("no embedding returned")
}

// BatchEmbed drives the HTTP call(s) for a batch of texts, honoring the
// spec's batchMode.
func (e *httpEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.spec.batchMode == batchOne {
		out := make([][]float32, len(texts))
		for i, t := range texts {
			res, err := e.doOne(ctx, []string{t})
			if err != nil {
				return nil, err
			}
			if len(res) > 0 {
				out[i] = res[0]
			}
		}
		return out, nil
	}
	return e.doOne(ctx, texts)
}

// doOne performs one HTTP call for the given batch and returns its results.
func (e *httpEmbedder) doOne(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := e.spec.buildBody(ctx, e, texts)
	if err != nil {
		logger.GetLogger(ctx).Errorf("%sEmbedder marshal request error: %v", e.spec.providerName, err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logger.GetLogger(ctx).Debugf("%sEmbedder BatchEmbed: model=%s, input_count=%d",
		e.spec.providerName, e.modelName, len(texts))

	authHeaders := defaultAuthHeaders(e.apiKey)
	if e.spec.buildAuthHeaders != nil {
		authHeaders = e.spec.buildAuthHeaders(e)
	}

	resp, err := httpx.DoPOST(ctx, httpx.POSTRequest{
		URL:           e.spec.buildURL(e),
		Body:          body,
		Headers:       authHeaders,
		MaxRetries:    e.maxRetries,
		HTTPClient:    e.httpClient,
		CustomHeaders: e.customHeaders,
		LogPrefix:     e.spec.providerName + "Embedder",
	})
	if err != nil {
		logger.GetLogger(ctx).Errorf("%sEmbedder BatchEmbed send request error: %v", e.spec.providerName, err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("%sEmbedder BatchEmbed read response error: %v", e.spec.providerName, err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if e.spec.formatError != nil {
			return nil, e.spec.formatError(respBody, resp.Status)
		}
		bodyStr := string(respBody)
		const maxBodyLog = 1000
		if len(bodyStr) > maxBodyLog {
			bodyStr = bodyStr[:maxBodyLog] + "... (truncated)"
		}
		logger.GetLogger(ctx).Errorf("%sEmbedder BatchEmbed API error: Http Status %s, Response Body: %s",
			e.spec.providerName, resp.Status, bodyStr)
		return nil, fmt.Errorf("EmbedBatch API error: Http Status %s, Response: %s", resp.Status, bodyStr)
	}

	result, err := e.spec.parseBody(respBody, texts)
	if err != nil {
		logger.GetLogger(ctx).Errorf("%sEmbedder BatchEmbed unmarshal response error: %v", e.spec.providerName, err)
		return nil, err
	}
	return result, nil
}

func (e *httpEmbedder) GetModelName() string { return e.modelName }
func (e *httpEmbedder) GetDimensions() int   { return e.dimensions }
func (e *httpEmbedder) GetModelID() string   { return e.modelID }

// defaultAuthHeaders returns Bearer-token auth, matching every provider
// except Azure (which overrides via buildAuthHeaders).
func defaultAuthHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
	}
}

// openAIResponse is the shape shared by OpenAI / Jina / Nvidia / Azure.
type openAIResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// parseOpenAIResponse extracts embeddings from the standard OpenAI-style
// response. Providers that follow this shape point parseBody here.
func parseOpenAIResponse(body []byte, _ []string) ([][]float32, error) {
	var response openAIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	out := make([][]float32, 0, len(response.Data))
	for _, d := range response.Data {
		out = append(out, d.Embedding)
	}
	return out, nil
}

// marshalJSON is a tiny helper that lets specs build request bodies via a
// map or a struct without repeating bytes.Buffer boilerplate.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	// json.Encoder appends a newline; trim it to match json.Marshal output.
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}
