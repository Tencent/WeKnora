package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/modelcache"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type persistentCacheEmbedder struct{ calls int }

func (e *persistentCacheEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.calls++
	return []float32{1, 2}, nil
}

func (e *persistentCacheEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{float32(i + 1), 2}
	}
	return result, nil
}

func (e *persistentCacheEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}
func (*persistentCacheEmbedder) GetModelName() string { return "embedding" }
func (*persistentCacheEmbedder) GetDimensions() int   { return 2 }
func (*persistentCacheEmbedder) GetModelID() string   { return "model-1" }

func TestEmbeddingCacheRepositoryPersistsAcrossSQLiteReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&types.EmbeddingCacheEntry{}))
		return db
	}
	prefix := modelcache.CachePrefix{
		TenantID: 7, ModelID: "model-1", ModelFingerprint: "fingerprint", RequestOptionsSHA256: "options",
	}
	now := time.Now().UTC()
	firstDB := open()
	first := NewEmbeddingCacheRepository(firstDB)
	require.NoError(t, first.PutEmbeddingCache(context.Background(), []*types.EmbeddingCacheEntry{{
		TenantID: 7, ModelID: "model-1", ModelFingerprint: "fingerprint", RequestOptionsSHA256: "options",
		TextSHA256: "text", Embedding: []byte{1, 2, 3, 4}, Dimension: 1,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, AccessedAt: now,
	}}))
	sqlDB, err := firstDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	secondDB := open()
	second := NewEmbeddingCacheRepository(secondDB)
	entries, err := second.GetEmbeddingCache(context.Background(), prefix, []string{"text"})
	require.NoError(t, err)
	require.Contains(t, entries, "text")
	require.Equal(t, []byte{1, 2, 3, 4}, entries["text"].Embedding)
}

func TestEmbeddingCacheHotCorpusAfterSQLiteReopenHasZeroProviderInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hot-cache.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&types.EmbeddingCacheEntry{}))
		return db
	}
	provider := &persistentCacheEmbedder{}
	model := &types.Model{ID: "model-1", TenantID: 7, Name: "embedding", Type: types.ModelTypeEmbedding}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	firstDB := open()
	first := modelcache.NewCoordinator(NewEmbeddingCacheRepository(firstDB)).Wrap(model, provider)
	firstVectors, err := first.BatchEmbed(ctx, []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, provider.calls)
	firstSQL, err := firstDB.DB()
	require.NoError(t, err)
	require.NoError(t, firstSQL.Close())

	secondDB := open()
	second := modelcache.NewCoordinator(NewEmbeddingCacheRepository(secondDB)).Wrap(model, provider)
	secondVectors, err := second.BatchEmbed(ctx, []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	require.Equal(t, 1, provider.calls, "second full corpus must send zero provider input items")
	require.Equal(t, firstVectors, secondVectors)
}
