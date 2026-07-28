package embedding

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/testutil/artifactrepo"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingEmbedder struct {
	mu          sync.Mutex
	batches     [][]string
	dimensions  int
	shortResult bool
	invalid     float32
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	values, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return values[0], nil
}

func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.batches = append(e.batches, append([]string(nil), texts...))
	e.mu.Unlock()
	count := len(texts)
	if e.shortResult && count > 0 {
		count--
	}
	result := make([][]float32, count)
	for index := range result {
		result[index] = make([]float32, e.dimensions)
		for dimension := range result[index] {
			result[index][dimension] = float32(len(texts[index]) + dimension)
		}
		if e.invalid != 0 {
			result[index][0] = e.invalid
		}
	}
	return result, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ Embedder,
	texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *countingEmbedder) GetModelName() string { return "model" }
func (e *countingEmbedder) GetDimensions() int   { return e.dimensions }
func (e *countingEmbedder) GetModelID() string   { return "model-id" }

func (e *countingEmbedder) calls() [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([][]string, len(e.batches))
	for index := range e.batches {
		result[index] = append([]string(nil), e.batches[index]...)
	}
	return result
}

func setupEmbeddingArtifactRuntime(t *testing.T) (*artifact.Runtime, *artifactrepo.Repository) {
	t.Helper()
	repository := artifactrepo.New()
	return artifact.NewRuntime(repository, nil), repository
}

func embeddingArtifactConfig() ArtifactCacheConfig {
	return ArtifactCacheConfig{
		TenantID: 1,
		Processor: artifact.ProcessorIdentity{
			ModelID:   "model-id",
			ModelName: "model",
			Source:    "remote",
			Provider:  "openai",
			Parameters: map[string]any{
				"dimensions": 3,
			},
		},
		Dimensions: 3,
	}
}

func documentEmbeddingContext() context.Context {
	return context.WithValue(context.Background(), types.EmbedDocumentContextKey, true)
}

func TestArtifactCachedEmbeddingDeduplicatesAndPreservesExactInputOrder(t *testing.T) {
	runtime, _ := setupEmbeddingArtifactRuntime(t)
	provider := &countingEmbedder{dimensions: 3}
	cached := NewArtifactCachedEmbedder(provider, runtime, embeddingArtifactConfig())
	inputs := []string{" alpha \r\n", "beta", " alpha \r\n"}

	first, err := cached.BatchEmbed(documentEmbeddingContext(), inputs)
	require.NoError(t, err)
	require.Len(t, first, 3)
	assert.Equal(t, first[0], first[2])
	assert.Equal(t, [][]string{{" alpha \r\n", "beta"}}, provider.calls())

	second, err := cached.BatchEmbed(documentEmbeddingContext(), inputs)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Len(t, provider.calls(), 1)
}

func TestArtifactCachedEmbeddingUsesPartialHits(t *testing.T) {
	runtime, _ := setupEmbeddingArtifactRuntime(t)
	provider := &countingEmbedder{dimensions: 3}
	cached := NewArtifactCachedEmbedder(provider, runtime, embeddingArtifactConfig())
	ctx := documentEmbeddingContext()

	_, err := cached.Embed(ctx, "cached")
	require.NoError(t, err)
	provider.mu.Lock()
	provider.batches = nil
	provider.mu.Unlock()

	vectors, err := cached.BatchEmbed(ctx, []string{"cached", "missing"})
	require.NoError(t, err)
	require.Len(t, vectors, 2)
	assert.Equal(t, [][]string{{"missing"}}, provider.calls())
}

func TestArtifactCachedEmbeddingBypassesInteractiveQuery(t *testing.T) {
	runtime, repository := setupEmbeddingArtifactRuntime(t)
	provider := &countingEmbedder{dimensions: 3}
	cached := NewArtifactCachedEmbedder(provider, runtime, embeddingArtifactConfig())
	ctx := context.WithValue(documentEmbeddingContext(), types.EmbedQueryContextKey, true)

	_, err := cached.Embed(ctx, "query")
	require.NoError(t, err)
	_, err = cached.Embed(ctx, "query")
	require.NoError(t, err)
	assert.Len(t, provider.calls(), 2)

	assert.Zero(t, repository.Count())
}

func TestArtifactCachedEmbeddingRejectsShortAndNonFiniteResponses(t *testing.T) {
	runtime, _ := setupEmbeddingArtifactRuntime(t)
	short := &countingEmbedder{dimensions: 3, shortResult: true}
	cached := NewArtifactCachedEmbedder(short, runtime, embeddingArtifactConfig())
	_, err := cached.BatchEmbed(documentEmbeddingContext(), []string{"one", "two"})
	require.ErrorContains(t, err, "returned 1 vectors for 2 inputs")

	nonFinite := &countingEmbedder{dimensions: 3, invalid: float32(math.Inf(1))}
	cached = NewArtifactCachedEmbedder(nonFinite, runtime, embeddingArtifactConfig())
	_, err = cached.BatchEmbed(documentEmbeddingContext(), []string{"three"})
	require.ErrorContains(t, err, "not finite")
}

func TestArtifactCacheConfigExcludesCredentialsAndUnrelatedMetadata(t *testing.T) {
	model := &types.Model{
		ID:       "model-id",
		Name:     "model",
		Source:   types.ModelSourceRemote,
		TenantID: 1,
		Parameters: types.ModelParameters{
			BaseURL:  "https://example.com/v1",
			APIKey:   "first-secret",
			Provider: "openai",
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension: 3,
			},
		},
	}
	first, ok := ArtifactCacheConfigFromModel(model, 1)
	require.True(t, ok)
	model.Parameters.APIKey = "rotated-secret"
	model.Description = "unrelated metadata"
	second, ok := ArtifactCacheConfigFromModel(model, 1)
	require.True(t, ok)

	assert.Equal(t, first, second)
}
