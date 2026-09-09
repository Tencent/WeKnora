package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEvaluationStoragePersistsAcrossInstances(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}))

	ctx := context.Background()
	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	params := &types.ChatManage{}
	params.Query = "What is WeKnora?"
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID: "evaluation-test", TenantID: 7, DatasetID: "dataset-1",
			StartTime: startedAt, Status: types.EvaluationStatuePending,
		},
		Params: params,
	}

	require.NoError(t, newEvaluationStorage(db).register(ctx, detail))

	// A fresh storage instance simulates reading the task after a service restart.
	restartedStorage := newEvaluationStorage(db)
	loaded, err := restartedStorage.get(ctx, detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, detail.Task.ID, loaded.Task.ID)
	require.Equal(t, detail.Task.TenantID, loaded.Task.TenantID)
	require.Equal(t, detail.Params.Query, loaded.Params.Query)
	require.Nil(t, loaded.Metric)
	require.Zero(t, loaded.Usage.CallCount)
	require.Empty(t, loaded.ModelCalls)

	finishedAt := startedAt.Add(1250 * time.Millisecond)
	require.NoError(t, restartedStorage.update(ctx, detail.Task.ID, func(current *types.EvaluationDetail) {
		current.Task.Status = types.EvaluationStatueSuccess
		current.Task.EndTime = &finishedAt
		current.Task.DurationMS = 1250
		current.Task.Total = 1
		current.Task.Finished = 1
		current.Metric = &types.MetricResult{
			RetrievalMetrics: types.RetrievalMetrics{Recall: 1},
		}
	}))

	loaded, err = newEvaluationStorage(db).get(ctx, detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, types.EvaluationStatueSuccess, loaded.Task.Status)
	require.EqualValues(t, 1250, loaded.Task.DurationMS)
	require.Equal(t, 1.0, loaded.Metric.RetrievalMetrics.Recall)
}

func TestEvaluationStorageReturnsTaskNotFound(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}))

	_, err = newEvaluationStorage(db).get(context.Background(), "missing")
	require.EqualError(t, err, "task not found")
}

func TestEvaluationStorageAggregatesModelCalls(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}))

	ctx := context.Background()
	startedAt := time.Now().UTC()
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID: "evaluation-usage", TenantID: 8, DatasetID: "dataset-2",
			StartTime: startedAt, Status: types.EvaluationStatueRunning,
		},
		Params: &types.ChatManage{},
	}
	storage := newEvaluationStorage(db)
	require.NoError(t, storage.register(ctx, detail))

	require.NoError(t, storage.recordModelCall(ctx, detail.Task.ID, detail.Task.TenantID, types.LLMCallObservation{
		ModelID: "model-1", ModelName: "test-model", Purpose: "query_rewrite",
		PromptPrefixFingerprint: "prefix-a", DurationMS: 100, Success: true,
		Usage: types.TokenUsage{
			PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
			CachedTokens: 40, CacheReadTokens: 40, CacheMissTokens: 60,
			CacheReported: true, CacheStatus: types.PromptCacheStatusHit,
		},
		Pricing: types.LLMTokenPricing{
			Enabled: true, Currency: "USD", InputPerMillion: 2,
			OutputPerMillion: 10, CacheReadPerMillion: 1,
		},
		EstimatedCost: 0.0012,
	}))
	require.NoError(t, storage.recordModelCall(ctx, detail.Task.ID, detail.Task.TenantID, types.LLMCallObservation{
		ModelID: "model-1", ModelName: "test-model", Purpose: "knowledge_qa",
		DurationMS: 300, Success: false, Error: "provider timeout",
		Usage: types.TokenUsage{
			PromptTokens: 50, CompletionTokens: 0, TotalTokens: 50,
			CacheMissTokens: 50, CacheReported: true, CacheStatus: types.PromptCacheStatusMiss,
		},
	}))

	loaded, err := storage.get(ctx, detail.Task.ID)
	require.NoError(t, err)
	require.Len(t, loaded.ModelCalls, 2)
	require.Equal(t, "query_rewrite", loaded.ModelCalls[0].Purpose)
	require.Equal(t, "provider timeout", loaded.ModelCalls[1].Error)
	require.Equal(t, 2, loaded.Usage.CallCount)
	require.Equal(t, 1, loaded.Usage.SuccessfulCalls)
	require.Equal(t, 1, loaded.Usage.FailedCalls)
	require.Equal(t, 150, loaded.Usage.PromptTokens)
	require.Equal(t, 170, loaded.Usage.TotalTokens)
	require.EqualValues(t, 400, loaded.Usage.ModelDurationMS)
	require.Equal(t, 200.0, loaded.Usage.AverageModelLatencyMS)
	require.InDelta(t, float64(40)/150, loaded.Usage.CacheHitRate, 0.0001)
	require.Equal(t, 1, loaded.Usage.PricedCalls)
	require.Equal(t, 1, loaded.Usage.UnpricedCalls)
	require.InDelta(t, 0.0012, loaded.Usage.CostByCurrency["USD"], 0.0000001)
	require.True(t, loaded.ModelCalls[0].Pricing.Enabled)
	require.Equal(t, "USD", loaded.ModelCalls[0].Pricing.Currency)
	require.InDelta(t, 0.0012, loaded.ModelCalls[0].EstimatedCost, 0.0000001)

	otherTenant := &types.EvaluationDetail{
		Task: &types.EvaluationTask{
			ID: "evaluation-other-tenant", TenantID: 9, DatasetID: "dataset-3",
			StartTime: startedAt, Status: types.EvaluationStatueRunning,
		},
		Params: &types.ChatManage{},
	}
	require.NoError(t, storage.register(ctx, otherTenant))
	require.NoError(t, storage.recordModelCall(ctx, otherTenant.Task.ID, otherTenant.Task.TenantID,
		types.LLMCallObservation{ModelID: "other-model", ModelName: "other", Success: true}))

	stats, err := storage.modelUsage(ctx, detail.Task.TenantID)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "model-1", stats[0].ModelID)
	require.Equal(t, 2, stats[0].Usage.CallCount)
	require.Equal(t, 150, stats[0].Usage.PromptTokens)
	require.InDelta(t, 0.0012, stats[0].Usage.CostByCurrency["USD"], 0.0000001)
}
