package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func topic3TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.EvaluationRun{}, &types.ModelUsage{}, &types.EmbeddingCacheEntry{}))
	return db
}

func TestEvaluationRepositoryPersistsAndScopesTenant(t *testing.T) {
	db := topic3TestDB(t)
	repo := NewEvaluationRepository(db)
	ctx := context.Background()
	detail := &types.EvaluationDetail{Task: &types.EvaluationTask{
		ID: "eval-1", TenantID: 7, DatasetID: "sample", Status: types.EvaluationStatueRunning, StartTime: time.Now(),
	}, Snapshot: &types.EvaluationSnapshot{DatasetSHA256: "abc", Concurrency: 1}, Metric: &types.MetricResult{RetrievalMetrics: types.RetrievalMetrics{Recall: .9}}}
	require.NoError(t, repo.Create(ctx, detail))

	loaded, err := repo.Get(ctx, 7, "eval-1")
	require.NoError(t, err)
	assert.Equal(t, .9, loaded.Metric.RetrievalMetrics.Recall)
	require.NotNil(t, loaded.Snapshot)
	assert.Equal(t, "abc", loaded.Snapshot.DatasetSHA256)
	assert.Equal(t, 1, loaded.Snapshot.Concurrency)
	_, err = repo.Get(ctx, 8, "eval-1")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, repo.MarkRunningInterrupted(ctx))
	loaded, err = repo.Get(ctx, 7, "eval-1")
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueFailed, loaded.Task.Status)
	assert.Contains(t, loaded.Task.ErrMsg, "restart")
}

func TestEmbeddingCacheAndUsageSummary(t *testing.T) {
	db := topic3TestDB(t)
	cache := NewEmbeddingCacheRepository(db)
	usage := NewModelUsageRepository(db)
	ctx := context.Background()
	encoded, _ := json.Marshal([]float32{1, 2})
	require.NoError(t, cache.Put(ctx, []*types.EmbeddingCacheEntry{{TenantID: 7, ModelKey: "m1", InputHash: "h1", Vector: types.JSON(encoded), Dimensions: 2}}))
	rows, err := cache.Get(ctx, 7, "m1", []string{"h1", "missing"})
	require.NoError(t, err)
	assert.Equal(t, []float32{1, 2}, rows["h1"])
	otherTenant, err := cache.Get(ctx, 8, "m1", []string{"h1"})
	require.NoError(t, err)
	assert.Empty(t, otherTenant)

	cost := .012
	require.NoError(t, usage.Create(ctx, &types.ModelUsage{TenantID: 7, Model: "qwen", CallType: "chat", ActualCalls: 1, PromptTokens: 100, CacheReadTokens: 40, CacheReported: true, CostCNY: &cost, Status: "success"}))
	require.NoError(t, usage.Create(ctx, &types.ModelUsage{TenantID: 7, Model: "rerank", CallType: "rerank", ActualCalls: 1, InputCount: 5, Status: "failed"}))
	require.NoError(t, usage.Create(ctx, &types.ModelUsage{TenantID: 7, Model: "embed", CallType: "embedding", ActualCalls: 0, InputCount: 4, LocalCacheHits: 3, Status: "success"}))
	summary, err := usage.Summary(ctx, 7, interfaces.ModelUsageQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), summary.Calls)
	assert.Equal(t, int64(1), summary.SuccessfulCalls)
	assert.Equal(t, int64(1), summary.FailedCalls)
	assert.Equal(t, int64(3), summary.EmbeddingCacheHits)
	require.NotNil(t, summary.EmbeddingCacheHitRate)
	assert.InDelta(t, .75, *summary.EmbeddingCacheHitRate, .0001)
	assert.InDelta(t, .4, *summary.ProviderCacheHitRate, .0001)
	assert.InDelta(t, cost, summary.EstimatedCostCNY, .0001)
}
