package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}, &embeddingCacheUsageRecord{}))

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
		RunConfig: &types.EvaluationRunConfig{
			SchemaVersion: 1, DatasetID: "dataset-1", DatasetFingerprint: "sha256:test",
			DatasetSamples: 1, CodeVersion: "commit-test",
		},
	}

	require.NoError(t, newEvaluationStorage(db).register(ctx, detail))

	// A fresh storage instance simulates reading the task after a service restart.
	restartedStorage := newEvaluationStorage(db)
	loaded, err := restartedStorage.get(ctx, detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, detail.Task.ID, loaded.Task.ID)
	require.Equal(t, detail.Task.TenantID, loaded.Task.TenantID)
	require.Equal(t, detail.Params.Query, loaded.Params.Query)
	require.Equal(t, detail.RunConfig, loaded.RunConfig)
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

func TestModelUsagePreservesCostsForMultipleModels(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "model-usage.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}, &embeddingCacheUsageRecord{}))
	storage := newEvaluationStorage(db)
	ctx := context.Background()

	for _, call := range []types.LLMCallObservation{
		{
			ModelID: "model-a", ModelName: "Model A", Success: true,
			Pricing: types.LLMTokenPricing{Enabled: true, Currency: "USD"}, EstimatedCost: 0.1,
		},
		{
			ModelID: "model-b", ModelName: "Model B", Success: true,
			Pricing: types.LLMTokenPricing{Enabled: true, Currency: "CNY"}, EstimatedCost: 0.2,
		},
	} {
		require.NoError(t, storage.recordModelCall(ctx, "task", 7, call))
	}

	stats, err := storage.modelUsage(ctx, 7, nil, nil)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	byID := map[string]types.ModelUsageStat{}
	for _, stat := range stats {
		byID[stat.ModelID] = stat
	}
	require.InDelta(t, 0.1, byID["model-a"].Usage.CostByCurrency["USD"], 0.000001)
	require.InDelta(t, 0.2, byID["model-b"].Usage.CostByCurrency["CNY"], 0.000001)
}

func TestModelUsageIncludesTenantScopedEmbeddingCache(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "embedding-cache-usage.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationModelCallRecord{}, &embeddingCacheUsageRecord{}))
	now := time.Now().UTC()
	require.NoError(t, db.Create([]embeddingCacheUsageRecord{
		{ID: "cache-1", TenantID: 7, ModelID: "embed-1", ModelName: "Embedding A", LookupCount: 5, HitCount: 2, MissCount: 2, DeduplicatedCount: 1, AvoidedComputations: 3, CreatedAt: now},
		{ID: "cache-2", TenantID: 7, ModelID: "embed-1", ModelName: "Embedding A", LookupCount: 3, HitCount: 1, MissCount: 2, AvoidedComputations: 1, CreatedAt: now},
		{ID: "other-tenant", TenantID: 8, ModelID: "embed-1", ModelName: "Embedding A", LookupCount: 100, HitCount: 100, AvoidedComputations: 100, CreatedAt: now},
	}).Error)

	stats, err := newEvaluationStorage(db).modelUsage(context.Background(), 7, nil, nil)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "embed-1", stats[0].ModelID)
	require.Equal(t, types.ModelTypeEmbedding, stats[0].ModelType)
	require.NotNil(t, stats[0].EmbeddingCache)
	require.Equal(t, 8, stats[0].EmbeddingCache.LookupCount)
	require.Equal(t, 3, stats[0].EmbeddingCache.HitCount)
	require.Equal(t, 4, stats[0].EmbeddingCache.MissCount)
	require.Equal(t, 1, stats[0].EmbeddingCache.DeduplicatedCount)
	require.Equal(t, 4, stats[0].EmbeddingCache.AvoidedComputations)
	require.InDelta(t, 0.375, stats[0].EmbeddingCache.HitRate, 0.000001)
	require.InDelta(t, 0.5, stats[0].EmbeddingCache.AvoidedRate, 0.000001)
}

func TestEvaluationStorageReturnsTaskNotFound(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}, &embeddingCacheUsageRecord{}))

	_, err = newEvaluationStorage(db).get(context.Background(), "missing")
	require.EqualError(t, err, "task not found")
}

func TestEvaluationStorageAggregatesModelCalls(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "evaluation-test-secret")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationRecord{}, &evaluationModelCallRecord{}, &embeddingCacheUsageRecord{}))

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
		ModelType: types.ModelTypeKnowledgeQA,
		ModelID:   "model-1", ModelName: "test-model", Purpose: "query_rewrite",
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
		ModelType: types.ModelTypeEmbedding,
		ModelID:   "model-1", ModelName: "test-model", Purpose: "knowledge_qa",
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
	require.Equal(t, types.ModelTypeKnowledgeQA, loaded.ModelCalls[0].ModelType)
	require.Equal(t, types.ModelTypeEmbedding, loaded.ModelCalls[1].ModelType)
	require.Empty(t, loaded.ModelCalls[1].Error)
	require.Equal(t, 2, loaded.Usage.CallCount)
	require.Equal(t, 1, loaded.Usage.SuccessfulCalls)
	require.Equal(t, 1, loaded.Usage.FailedCalls)
	require.Equal(t, 150, loaded.Usage.PromptTokens)
	require.Equal(t, 170, loaded.Usage.TotalTokens)
	require.EqualValues(t, 400, loaded.Usage.ModelDurationMS)
	require.Equal(t, 200.0, loaded.Usage.AverageModelLatencyMS)
	require.InDelta(t, float64(40)/150, loaded.Usage.CacheHitRate, 0.0001)
	require.InDelta(t, 1, loaded.Usage.CacheCoverageRate, 0.0001)
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

	stats, err := storage.modelUsage(ctx, detail.Task.TenantID, nil, nil)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "model-1", stats[0].ModelID)
	require.Equal(t, 2, stats[0].Usage.CallCount)
	require.Equal(t, 150, stats[0].Usage.PromptTokens)
	require.InDelta(t, 0.0012, stats[0].Usage.CostByCurrency["USD"], 0.0000001)
	require.Len(t, stats[0].Purposes, 2)
	purposes := map[string]types.EvaluationUsage{}
	for _, purpose := range stats[0].Purposes {
		purposes[purpose.Purpose] = purpose.Usage
	}
	require.Equal(t, 1, purposes["query_rewrite"].CallCount)
	require.Equal(t, 120, purposes["query_rewrite"].TotalTokens)
	require.InDelta(t, 0.4, purposes["query_rewrite"].CacheHitRate, 0.0001)
	require.InDelta(t, 0.0012, purposes["query_rewrite"].CostByCurrency["USD"], 0.0000001)
	require.Equal(t, 1, purposes["knowledge_qa"].CallCount)
	require.Equal(t, 1, purposes["knowledge_qa"].FailedCalls)
	require.Empty(t, purposes["knowledge_qa"].CostByCurrency)

	require.NoError(t, storage.db.Model(&evaluationModelCallRecord{}).
		Where("purpose = ?", "query_rewrite").
		Update("created_at", startedAt.Add(-48*time.Hour)).Error)
	windowStart := startedAt.Add(-time.Hour)
	windowStats, err := storage.modelUsage(ctx, detail.Task.TenantID, &windowStart, nil)
	require.NoError(t, err)
	require.Len(t, windowStats, 1)
	require.Equal(t, 1, windowStats[0].Usage.CallCount)
	require.Equal(t, 50, windowStats[0].Usage.PromptTokens)
	require.Empty(t, windowStats[0].Usage.CostByCurrency)
	require.Len(t, windowStats[0].Purposes, 1)
	require.Equal(t, "knowledge_qa", windowStats[0].Purposes[0].Purpose)
	require.Equal(t, 50, windowStats[0].Purposes[0].Usage.PromptTokens)

	finishedAt := startedAt.Add(time.Minute)
	require.NoError(t, storage.update(ctx, detail.Task.ID, func(current *types.EvaluationDetail) {
		current.Task.Status = types.EvaluationStatueSuccess
		current.Task.EndTime = &finishedAt
	}))
	require.NoError(t, storage.db.Where("task_id = ?", detail.Task.ID).
		Delete(&evaluationModelCallRecord{}).Error)
	afterRetention, err := newEvaluationStorage(db).get(ctx, detail.Task.ID)
	require.NoError(t, err)
	require.Empty(t, afterRetention.ModelCalls)
	require.NotNil(t, afterRetention.Usage)
	require.Equal(t, 2, afterRetention.Usage.CallCount)
	require.Equal(t, 170, afterRetention.Usage.TotalTokens)
	require.InDelta(t, 0.0012, afterRetention.Usage.CostByCurrency["USD"], 0.0000001)
	require.NoError(t, newEvaluationStorage(db).update(ctx, detail.Task.ID, func(current *types.EvaluationDetail) {
		current.Task.Finished = current.Task.Total
	}))
	afterTerminalRewrite, err := newEvaluationStorage(db).get(ctx, detail.Task.ID)
	require.NoError(t, err)
	require.Equal(t, 2, afterTerminalRewrite.Usage.CallCount)
	require.Equal(t, 170, afterTerminalRewrite.Usage.TotalTokens)
}

func TestEvaluationStorageProtectsFingerprintAndDropsProviderError(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "evaluation-secret")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "evaluation-privacy.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationModelCallRecord{}))
	storage := newEvaluationStorage(db)

	require.NoError(t, storage.recordModelCall(t.Context(), "task-privacy", 17, types.LLMCallObservation{
		ModelName: "privacy-model", PromptPrefixFingerprint: "raw-stable-prefix",
		Error: "provider error containing a request excerpt", Success: false,
	}))

	var record evaluationModelCallRecord
	require.NoError(t, db.First(&record).Error)
	mac := hmac.New(sha256.New, []byte("evaluation-secret"))
	_, _ = mac.Write([]byte("raw-stable-prefix"))
	require.Equal(t, hex.EncodeToString(mac.Sum(nil)), record.PromptPrefixFingerprint)
	require.NotEqual(t, "raw-stable-prefix", record.PromptPrefixFingerprint)
	require.Empty(t, record.ErrMsg)

	// A missing key must fail closed: omit the fingerprint instead of storing a
	// weak, guessable hash.
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "")
	unkeyedStorage := newEvaluationStorage(db)
	require.NoError(t, unkeyedStorage.recordModelCall(t.Context(), "task-no-key", 17, types.LLMCallObservation{
		ModelName: "no-key-model", PromptPrefixFingerprint: "must-not-be-stored", Success: true,
	}))
	var unkeyedRecord evaluationModelCallRecord
	require.NoError(t, db.Where("model_name = ?", "no-key-model").First(&unkeyedRecord).Error)
	require.Empty(t, unkeyedRecord.PromptPrefixFingerprint)
}
