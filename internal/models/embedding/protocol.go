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
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
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
	resolved, err := modelruntime.Resolve(modelruntime.Ref{
		Provider:             config.Provider,
		Model:                config.ModelName,
		BaseURL:              config.BaseURL,
		ModelType:            types.ModelTypeEmbedding,
		Extra:                config.ExtraConfig,
		Override:             config.Spec,
		TruncatePromptTokens: config.TruncatePromptTokens,
	})
	if err != nil {
		return nil, err
	}
	if err := validateEmbeddingBaseURL(resolved.BaseURL); err != nil {
		return nil, err
	}

	vendor := resolved.Vendor
	endpoint, err := resolved.Endpoint(types.ModelTypeEmbedding, modelruntime.Connection{
		ModelID:     config.ModelID,
		Credentials: api.Credentials{APIKey: config.APIKey, AppID: config.AppID, AppSecret: config.AppSecret},
		Headers:     config.CustomHeaders,
		Extra:       config.ExtraConfig,
		Client:      newEmbeddingHTTPClient(time.Duration(resolved.Embeddings.RequestTimeout) * time.Second),
	})
	if err != nil {
		return nil, err
	}
	if endpoint.URL != "" {
		if err := validateEmbeddingBaseURL(endpoint.URL); err != nil {
			return nil, err
		}
	}
	settings := resolved.Embeddings

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
	settings   api.EmbeddingsSettings
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
