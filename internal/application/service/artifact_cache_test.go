package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newArtifactTestCache(t *testing.T) *ArtifactCache {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.ProcessingArtifact{}))
	return NewArtifactCache(apprepo.NewProcessingArtifactRepository(db))
}

func TestArtifactCacheComputesOnceAcrossWorkers(t *testing.T) {
	cacheA := newArtifactTestCache(t)
	cacheB := &ArtifactCache{repo: cacheA.repo, instance: "worker-b", leaseTime: time.Minute}
	spec := ArtifactCacheSpec{
		TenantID:      7,
		Kind:          types.ProcessingArtifactSummary,
		InputHash:     hashBytes([]byte("same document")),
		SchemaVersion: "test-v1",
	}

	type result struct {
		Value string `json:"value"`
	}
	var calls atomic.Int32
	start := make(chan struct{})
	compute := func() (any, error) {
		calls.Add(1)
		<-start
		return result{Value: "ready"}, nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	values := make(chan string, 2)
	for _, cache := range []*ArtifactCache{cacheA, cacheB} {
		wg.Add(1)
		go func(cache *ArtifactCache) {
			defer wg.Done()
			var got result
			_, err := cache.GetOrComputeJSON(context.Background(), spec, &got, compute)
			values <- got.Value
			errs <- err
		}(cache)
	}

	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, 10*time.Millisecond)
	close(start)
	wg.Wait()
	close(errs)
	close(values)
	for err := range errs {
		require.NoError(t, err)
	}
	for value := range values {
		require.Equal(t, "ready", value)
	}
	require.Equal(t, int32(1), calls.Load())
}

type countingEmbedder struct {
	mu     sync.Mutex
	calls  int
	inputs [][]string
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
	defer e.mu.Unlock()
	e.calls++
	e.inputs = append(e.inputs, append([]string(nil), texts...))
	result := make([][]float32, len(texts))
	for i, text := range texts {
		result[i] = []float32{float32(len(text)), 1}
	}
	return result, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context, model embedding.Embedder, texts []string,
) ([][]float32, error) {
	return model.BatchEmbed(ctx, texts)
}

func (e *countingEmbedder) GetModelName() string { return "counting" }
func (e *countingEmbedder) GetModelID() string   { return "model-1" }
func (e *countingEmbedder) GetDimensions() int   { return 2 }

func TestCachedEmbedderDeduplicatesAndScopesByTenant(t *testing.T) {
	cache := newArtifactTestCache(t)
	inner := &countingEmbedder{}
	ctx := context.Background()

	tenantOne := wrapIngestionEmbedder(cache, 1, inner)
	first, err := tenantOne.BatchEmbed(ctx, []string{"alpha", "alpha", "beta"})
	require.NoError(t, err)
	require.Equal(t, first[0], first[1])
	require.Equal(t, 1, inner.calls)
	require.Len(t, inner.inputs[0], 2)

	second, err := tenantOne.BatchEmbed(ctx, []string{"beta", "alpha"})
	require.NoError(t, err)
	require.Len(t, second, 2)
	require.Equal(t, 1, inner.calls, "second ingestion should use cached vectors")

	tenantTwo := wrapIngestionEmbedder(cache, 2, inner)
	_, err = tenantTwo.BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls, "cache entries must not cross tenant boundaries")
}
