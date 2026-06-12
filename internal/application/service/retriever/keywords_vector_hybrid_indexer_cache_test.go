package retriever

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type countingEmbedder struct {
	calls int
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{1.0}, nil
}

func (e *countingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls++
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = []float32{1.0}
	}
	return embeddings, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(ctx context.Context, model embedding.Embedder, texts []string) ([][]float32, error) {
	e.calls++
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = []float32{1.0}
	}
	return embeddings, nil
}

func (e *countingEmbedder) GetModelName() string { return "test-model" }
func (e *countingEmbedder) GetDimensions() int   { return 3 }
func (e *countingEmbedder) GetModelID() string   { return "test-model-id" }

// TestBatchIndex_CachePreventsRepeatedEmbedding verifies that the embedding
// cache prevents redundant API calls: identical content is embedded once,
// and subsequent calls skip the embedding API entirely.
func TestBatchIndex_CachePreventsRepeatedEmbedding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&embedding.CacheEntry{}))

	cache := embedding.NewEmbeddingCache(db)
	embedder := &countingEmbedder{}

	engine := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: &saveOnlyRepository{},
		engineType:      types.PostgresRetrieverEngineType,
		embeddingCache:  cache,
	}

	chunks := []*types.IndexInfo{
		{Content: "chunk-1", SourceID: "s1", ChunkID: "c1"},
		{Content: "chunk-2", SourceID: "s2", ChunkID: "c2"},
		{Content: "chunk-3", SourceID: "s3", ChunkID: "c3"},
	}
	retrieverTypes := []types.RetrieverType{types.VectorRetrieverType}

	// First call: all miss, 1 BatchEmbedWithPool call for 3 chunks.
	err = engine.BatchIndex(context.Background(), embedder, chunks, retrieverTypes)
	require.NoError(t, err)
	assert.Equal(t, 1, embedder.calls, "first call should batch-embed all 3 chunks in one API call")

	// Second call: all hit, no additional API calls.
	prevCalls := embedder.calls
	err = engine.BatchIndex(context.Background(), embedder, chunks, retrieverTypes)
	require.NoError(t, err)
	assert.Equal(t, prevCalls, embedder.calls, "second call should hit cache, make 0 additional API calls")
}

// TestBatchIndex_CachePartialMiss verifies mixed hit/miss behavior:
// previously cached chunks skip the API while new chunks trigger embedding.
func TestBatchIndex_CachePartialMiss(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&embedding.CacheEntry{}))

	cache := embedding.NewEmbeddingCache(db)
	embedder := &countingEmbedder{}

	engine := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: &saveOnlyRepository{},
		engineType:      types.PostgresRetrieverEngineType,
		embeddingCache:  cache,
	}

	// Prime cache with 2 chunks.
	chunks1 := []*types.IndexInfo{
		{Content: "chunk-a", SourceID: "sa", ChunkID: "ca"},
		{Content: "chunk-b", SourceID: "sb", ChunkID: "cb"},
	}
	err = engine.BatchIndex(context.Background(), embedder, chunks1, typesRetrieverVector)
	require.NoError(t, err)
	assert.Equal(t, 1, embedder.calls)

	// Second batch: 2 old + 2 new.
	chunks2 := []*types.IndexInfo{
		{Content: "chunk-a", SourceID: "sa2", ChunkID: "ca2"}, // cached
		{Content: "chunk-c", SourceID: "sc", ChunkID: "cc"},    // new
		{Content: "chunk-b", SourceID: "sb2", ChunkID: "cb2"}, // cached
		{Content: "chunk-d", SourceID: "sd", ChunkID: "cd"},    // new
	}
	prevCalls := embedder.calls
	err = engine.BatchIndex(context.Background(), embedder, chunks2, typesRetrieverVector)
	require.NoError(t, err)
	assert.Equal(t, prevCalls+1, embedder.calls, "cache hits should skip API; only 2 new chunks need embedding")
}

// TestBatchIndex_CacheDisabledNoEffect verifies that when cache is nil the
// original embedding path is used with no change in behavior.
func TestBatchIndex_CacheDisabledNoEffect(t *testing.T) {
	embedder := &countingEmbedder{}

	engine := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: &saveOnlyRepository{},
		engineType:      types.PostgresRetrieverEngineType,
		embeddingCache:  nil,
	}

	chunks := []*types.IndexInfo{
		{Content: "chunk-x", SourceID: "sx", ChunkID: "cx"},
	}
	err := engine.BatchIndex(context.Background(), embedder, chunks, typesRetrieverVector)
	require.NoError(t, err)
	assert.Equal(t, 1, embedder.calls, "without cache, every call goes through embedding API")
}

var typesRetrieverVector = []types.RetrieverType{types.VectorRetrieverType}
