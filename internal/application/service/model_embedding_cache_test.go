package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

type embeddingCacheModelRepo struct {
	interfaces.ModelRepository
	model types.Model
}

func (r *embeddingCacheModelRepo) GetByID(_ context.Context, tenantID uint64, _ string) (*types.Model, error) {
	model := r.model
	model.TenantID = tenantID
	return &model, nil
}

func TestModelServiceEmbeddingCacheScopeAndInvalidation(t *testing.T) {
	t.Setenv("WEKNORA_EMBEDDING_CACHE_ENABLED", "true")
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,2]}]}`))
	}))
	defer server.Close()
	repo := &embeddingCacheModelRepo{model: types.Model{
		ID: "cache-model", Name: "embedding-test", Source: types.ModelSourceRemote,
		Status: types.ModelStatusActive, Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{BaseURL: server.URL, Provider: "openai"},
	}}
	repo.model.Parameters.EmbeddingParameters.Dimension = 2
	cache := embedding.NewEmbeddingResultCache(nil)
	service := NewModelServiceWithEmbeddingCache(repo, nil, nil, nil, nil, nil, cache)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	for range 2 {
		model, err := service.GetEmbeddingModel(ctx, repo.model.ID)
		require.NoError(t, err)
		_, err = model.Embed(ctx, "same text")
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load(), "reconstructed model clients must reuse the shared cache")
	shared, err := service.GetEmbeddingModelForTenant(ctx, repo.model.ID, 8)
	require.NoError(t, err)
	_, err = shared.Embed(ctx, "same text")
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load(), "shared model lookup must use its explicit tenant")
	repo.model.UpdatedAt = time.Unix(10, 0)
	changed, err := service.GetEmbeddingModel(ctx, repo.model.ID)
	require.NoError(t, err)
	_, err = changed.Embed(ctx, "same text")
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load(), "model updates must invalidate cached vectors")
}
