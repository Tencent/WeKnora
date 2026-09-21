package embedding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/arkembeddings"
	"github.com/Tencent/WeKnora/internal/models/api/dashscopeembeddings"
	"github.com/Tencent/WeKnora/internal/models/api/googleembeddings"
	"github.com/Tencent/WeKnora/internal/models/api/openaiembeddings"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

// retryPolicy is the transport-error retry budget; tests shorten it.
var retryPolicy = api.DefaultRetryPolicy

// newRemoteEmbedder resolves the catalog and returns the protocol client for
// the configured model, wrapped in the shared batching layer. It mirrors
// rerank.newReranker: the vendor's facts decide the protocol, the URL and the
// credential, and this function knows no vendor names.
func newRemoteEmbedder(config Config, pooler EmbedderPooler) (Embedder, error) {
	if strings.TrimSpace(config.ModelName) == "" {
		return nil, fmt.Errorf("model name is required")
	}
	resolved, err := catalog.Resolve(catalog.Ref{
		Provider:             config.Provider,
		Model:                config.ModelName,
		BaseURL:              config.BaseURL,
		ModelType:            types.ModelTypeEmbedding,
		Extra:                config.ExtraConfig,
		TruncatePromptTokens: config.TruncatePromptTokens,
	})
	if err != nil {
		return nil, err
	}
	if err := validateEmbeddingBaseURL(resolved.BaseURL); err != nil {
		return nil, err
	}

	vendor := resolved.Vendor
	creds := catalog.Credentials{APIKey: config.APIKey, AppID: config.AppID, AppSecret: config.AppSecret}
	if creds.APIKey == "" {
		creds.APIKey = vendor.DefaultAPIKey
	}
	// A signing vendor with no identity pair would otherwise send unsigned
	// requests and fail at the far end, which is a worse error than this one.
	if vendor.Auth == catalog.AuthSigned {
		if creds.AppID == "" {
			return nil, fmt.Errorf("%s embedding: AppID is required", vendor.Name)
		}
		if creds.AppSecret == "" {
			return nil, fmt.Errorf("%s embedding: AppSecret is required", vendor.Name)
		}
	}
	// Vendor headers first so a user header cannot silently replace a vendor
	// beta flag, matching chat.NewRemoteChat.
	headers := make(map[string]string, len(vendor.Headers)+len(config.CustomHeaders))
	for k, v := range vendor.Headers {
		headers[k] = v
	}
	for k, v := range config.CustomHeaders {
		headers[k] = v
	}
	settings := resolved.Embeddings
	endpoint := api.Endpoint{
		BaseURL: resolved.BaseURL,
		Model:   resolved.RemoteModel,
		ModelID: config.ModelID,
		Auth:    vendor.AuthFunc(vendor.API, creds),
		Headers: headers,
		Client:  newEmbeddingHTTPClient(time.Duration(settings.RequestTimeout) * time.Second),
	}
	if vendor.Endpoint != nil {
		if url, query := vendor.Endpoint(catalog.EndpointRequest{
			BaseURL:      resolved.BaseURL,
			Model:        resolved.RemoteModel,
			ModelType:    types.ModelTypeEmbedding,
			API:          vendor.API,
			EmbeddingAPI: resolved.EmbeddingAPI,
			Extra:        config.ExtraConfig,
		}); url != "" {
			if err := validateEmbeddingBaseURL(url); err != nil {
				return nil, err
			}
			endpoint.URL, endpoint.Query = url, query
		}
	}

	// The width is the vendor's field but the operator's decision: a row
	// that did not opt in keeps the model's native width even where the
	// vendor could narrow it.
	dimensions := 0
	if config.SupportsDimensionOverride {
		dimensions = config.Dimensions
	}
	retry := retryPolicy()

	var client api.Embedder
	switch resolved.EmbeddingAPI {
	case api.EmbeddingOpenAI:
		client = openaiembeddings.New(openaiembeddings.Config{
			Endpoint: endpoint, Settings: settings, Dimensions: dimensions, Retry: retry,
		})
	case api.EmbeddingDashScope:
		client = dashscopeembeddings.New(dashscopeembeddings.Config{
			Endpoint: endpoint, Settings: settings, Dimensions: dimensions, Retry: retry,
		})
	case api.EmbeddingArk:
		client = arkembeddings.New(arkembeddings.Config{
			Endpoint: endpoint, Settings: settings, Dimensions: dimensions, Retry: retry,
		})
	case api.EmbeddingGoogle:
		client = googleembeddings.New(googleembeddings.Config{
			Endpoint: endpoint, Settings: settings, Dimensions: dimensions, Retry: retry,
		})
	default:
		return nil, fmt.Errorf("unsupported embedding api %q for provider %s", resolved.EmbeddingAPI, vendor.ID)
	}

	return &protocolEmbedder{
		inner:          client,
		settings:       settings,
		modelName:      config.ModelName,
		modelID:        config.ModelID,
		dimensions:     config.Dimensions,
		EmbedderPooler: pooler,
	}, nil
}

// protocolEmbedder adapts a protocol client to the Embedder interface and
// owns the two things every vendor needs and none of them should implement
// itself: splitting a batch that exceeds the documented per-request ceiling,
// and telling the vendor which side of a search a text is on.
type protocolEmbedder struct {
	inner      api.Embedder
	settings   catalog.EmbeddingsSettings
	modelName  string
	modelID    string
	dimensions int
	EmbedderPooler
}

func (e *protocolEmbedder) GetModelName() string { return e.modelName }
func (e *protocolEmbedder) GetModelID() string   { return e.modelID }
func (e *protocolEmbedder) GetDimensions() int   { return e.dimensions }

func (e *protocolEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (e *protocolEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	kind := api.EmbedDocument
	if types.IsEmbedQuery(ctx) {
		kind = api.EmbedQuery
	}
	batches, err := api.SplitBatches(texts, 0, e.settings.BatchLimits())
	if err != nil {
		return nil, fmt.Errorf("%s embedding: %w", e.modelName, err)
	}
	out := make([][]float32, len(texts))
	// Serial on purpose. BatchEmbedWithPool and the per-model concurrency
	// gate already bound how many requests are in flight; fanning out again
	// here would multiply past both, and a one-text-per-request vendor would
	// turn every pool chunk into a burst.
	for _, batch := range batches {
		vectors, err := e.inner.Embed(ctx, batch.Items, kind)
		if err != nil {
			return nil, err
		}
		if len(vectors) != len(batch.Items) {
			return nil, fmt.Errorf(
				"%s embedding: %d vectors for %d inputs", e.modelName, len(vectors), len(batch.Items),
			)
		}
		copy(out[batch.Start:], vectors)
	}
	return out, nil
}
