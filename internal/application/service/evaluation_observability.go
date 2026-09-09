package service

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

type evaluationModelCallRecord struct {
	ID                        string          `gorm:"primaryKey;size:36"`
	TaskID                    *string         `gorm:"size:255;index"`
	TenantID                  uint64          `gorm:"not null;index"`
	ModelType                 types.ModelType `gorm:"size:32;not null"`
	ModelID                   string          `gorm:"size:255"`
	ModelName                 string          `gorm:"size:255;not null"`
	Purpose                   string          `gorm:"size:100"`
	PromptPrefixFingerprint   string          `gorm:"size:128"`
	PromptTokens              int
	CompletionTokens          int
	TotalTokens               int
	CachedTokens              int
	CacheReadTokens           int
	CacheWriteTokens          int
	CacheMissTokens           int
	CacheReported             bool
	CacheStatus               types.PromptCacheStatus `gorm:"size:32"`
	PricingConfigured         bool
	Currency                  string `gorm:"size:16"`
	InputPricePerMillion      float64
	OutputPricePerMillion     float64
	CacheReadPricePerMillion  float64
	CacheWritePricePerMillion float64
	EstimatedCost             float64
	DurationMS                int64
	Success                   bool   `gorm:"not null"`
	ErrMsg                    string `gorm:"type:text"`
	CreatedAt                 time.Time
}

func (evaluationModelCallRecord) TableName() string {
	return "evaluation_model_calls"
}

type embeddingCacheUsageRecord struct {
	ID                  string `gorm:"primaryKey;size:36"`
	TenantID            uint64 `gorm:"not null;index"`
	ModelID             string `gorm:"size:255;not null"`
	ModelName           string `gorm:"size:255;not null"`
	LookupCount         int
	HitCount            int
	MissCount           int
	DeduplicatedCount   int
	AvoidedComputations int
	CreatedAt           time.Time
}

func (embeddingCacheUsageRecord) TableName() string { return "embedding_cache_usage_events" }

type evaluationCallObserver struct {
	ctx      context.Context
	storage  *evaluationStorage
	taskID   string
	tenantID uint64
}

func (o *evaluationCallObserver) ObserveLLMCall(observation types.LLMCallObservation) {
	if err := o.storage.recordModelCall(o.ctx, o.taskID, o.tenantID, observation); err != nil {
		logger.Errorf(o.ctx, "Failed to persist evaluation model call: %v", err)
	}
}

func (e *evaluationStorage) recordModelCall(
	ctx context.Context,
	taskID string,
	tenantID uint64,
	observation types.LLMCallObservation,
) error {
	taskIDCopy := taskID
	// Evaluation calls use a request-scoped observer, so they do not pass
	// through ModelCallRecorder. Apply the same privacy policy here: never store
	// provider errors (which may contain request excerpts), and HMAC-protect the
	// stable prefix fingerprint with the deployment secret.
	observation.Error = ""
	observation.PromptPrefixFingerprint = protectModelCallFingerprint(
		observation.PromptPrefixFingerprint, e.fingerprintKey,
	)
	record := newModelCallRecord(&taskIDCopy, tenantID, observation, time.Now().UTC())
	return e.db.WithContext(ctx).Create(record).Error
}

func newModelCallRecord(
	taskID *string,
	tenantID uint64,
	observation types.LLMCallObservation,
	createdAt time.Time,
) *evaluationModelCallRecord {
	usage := observation.Usage
	pricing := observation.Pricing.Normalize()
	return &evaluationModelCallRecord{
		ID: uuid.NewString(), TaskID: taskID, TenantID: tenantID, ModelType: observation.ModelType,
		ModelID: observation.ModelID, ModelName: observation.ModelName,
		Purpose: observation.Purpose, PromptPrefixFingerprint: observation.PromptPrefixFingerprint,
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		TotalTokens: usage.TotalTokens, CachedTokens: usage.CachedTokens,
		CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
		CacheMissTokens: usage.CacheMissTokens, CacheReported: usage.CacheReported,
		CacheStatus: usage.CacheStatus, PricingConfigured: pricing.Enabled,
		Currency: pricing.Currency, InputPricePerMillion: pricing.InputPerMillion,
		OutputPricePerMillion:     pricing.OutputPerMillion,
		CacheReadPricePerMillion:  pricing.CacheReadPerMillion,
		CacheWritePricePerMillion: pricing.CacheWritePerMillion,
		EstimatedCost:             observation.EstimatedCost, DurationMS: observation.DurationMS,
		Success: observation.Success, ErrMsg: observation.Error, CreatedAt: createdAt,
	}
}

func (e *evaluationStorage) getModelCalls(
	ctx context.Context,
	taskID string,
) ([]types.EvaluationModelCall, *types.EvaluationUsage, error) {
	var records []evaluationModelCallRecord
	if err := e.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("created_at ASC, id ASC").
		Find(&records).Error; err != nil {
		return nil, nil, err
	}

	calls := make([]types.EvaluationModelCall, 0, len(records))
	usage := &types.EvaluationUsage{CostByCurrency: make(map[string]float64)}
	for _, record := range records {
		callUsage := types.TokenUsage{
			PromptTokens: record.PromptTokens, CompletionTokens: record.CompletionTokens,
			TotalTokens: record.TotalTokens, CachedTokens: record.CachedTokens,
			CacheReadTokens: record.CacheReadTokens, CacheWriteTokens: record.CacheWriteTokens,
			CacheMissTokens: record.CacheMissTokens, CacheReported: record.CacheReported,
			CacheStatus: record.CacheStatus,
		}
		calls = append(calls, types.EvaluationModelCall{
			ID: record.ID, ModelID: record.ModelID, ModelName: record.ModelName,
			ModelType: record.ModelType,
			Purpose:   record.Purpose, PromptPrefixFingerprint: record.PromptPrefixFingerprint,
			Usage: callUsage,
			Pricing: types.LLMTokenPricing{
				Enabled: record.PricingConfigured, Currency: record.Currency,
				InputPerMillion:      record.InputPricePerMillion,
				OutputPerMillion:     record.OutputPricePerMillion,
				CacheReadPerMillion:  record.CacheReadPricePerMillion,
				CacheWritePerMillion: record.CacheWritePricePerMillion,
			},
			EstimatedCost: record.EstimatedCost,
			DurationMS:    record.DurationMS, Success: record.Success,
			Error: record.ErrMsg, CreatedAt: record.CreatedAt,
		})
		accumulateEvaluationUsage(usage, record)
	}
	finalizeEvaluationUsage(usage)
	return calls, usage, nil
}

func accumulateEvaluationUsage(usage *types.EvaluationUsage, call evaluationModelCallRecord) {
	usage.CallCount++
	if call.Success {
		usage.SuccessfulCalls++
	} else {
		usage.FailedCalls++
	}
	usage.PromptTokens += call.PromptTokens
	usage.CompletionTokens += call.CompletionTokens
	usage.TotalTokens += call.TotalTokens
	usage.CacheReadTokens += call.CacheReadTokens
	usage.CacheWriteTokens += call.CacheWriteTokens
	usage.CacheMissTokens += call.CacheMissTokens
	usage.ModelDurationMS += call.DurationMS
	if call.PricingConfigured {
		usage.PricedCalls++
		usage.CostByCurrency[call.Currency] += call.EstimatedCost
	} else {
		usage.UnpricedCalls++
	}
	if call.CacheReported {
		usage.CacheReportedCalls++
		if call.CacheReadTokens > 0 {
			usage.CacheHitCalls++
		}
	}
}

func finalizeEvaluationUsage(usage *types.EvaluationUsage) {
	if usage.CallCount > 0 {
		usage.AverageModelLatencyMS = float64(usage.ModelDurationMS) / float64(usage.CallCount)
		usage.CacheCoverageRate = float64(usage.CacheReportedCalls) / float64(usage.CallCount)
	}
	reportedPromptTokens := usage.CacheReadTokens + usage.CacheMissTokens
	if reportedPromptTokens > 0 {
		usage.CacheHitRate = float64(usage.CacheReadTokens) / float64(reportedPromptTokens)
	}
}

type modelUsageAggregateRow struct {
	ModelID            string
	ModelName          string
	ModelType          types.ModelType
	Purpose            string
	CallCount          int
	SuccessfulCalls    int
	FailedCalls        int
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	CacheReadTokens    int
	CacheWriteTokens   int
	CacheMissTokens    int
	CacheReportedCalls int
	CacheHitCalls      int
	ModelDurationMS    int64
	PricedCalls        int
	UnpricedCalls      int
}

type modelCostAggregateRow struct {
	ModelID  string
	Purpose  string
	Currency string
	Cost     float64
}

type embeddingCacheAggregateRow struct {
	ModelID             string
	ModelName           string
	LookupCount         int
	HitCount            int
	MissCount           int
	DeduplicatedCount   int
	AvoidedComputations int
}

func usageFromAggregateRow(row modelUsageAggregateRow) types.EvaluationUsage {
	usage := types.EvaluationUsage{
		CallCount: row.CallCount, SuccessfulCalls: row.SuccessfulCalls,
		FailedCalls: row.FailedCalls, PromptTokens: row.PromptTokens,
		CompletionTokens: row.CompletionTokens, TotalTokens: row.TotalTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		CacheMissTokens: row.CacheMissTokens, CacheReportedCalls: row.CacheReportedCalls,
		CacheHitCalls: row.CacheHitCalls, ModelDurationMS: row.ModelDurationMS,
		PricedCalls: row.PricedCalls, UnpricedCalls: row.UnpricedCalls,
		CostByCurrency: make(map[string]float64),
	}
	finalizeEvaluationUsage(&usage)
	return usage
}

func (e *evaluationStorage) modelUsage(
	ctx context.Context,
	tenantID uint64,
	startTime, endTime *time.Time,
) ([]types.ModelUsageStat, error) {
	var rows []modelUsageAggregateRow
	usageQuery := e.db.WithContext(ctx).Model(&evaluationModelCallRecord{}).
		Where("tenant_id = ?", tenantID)
	if startTime != nil {
		usageQuery = usageQuery.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		usageQuery = usageQuery.Where("created_at <= ?", *endTime)
	}
	err := usageQuery.
		Select(`model_id, MAX(model_name) AS model_name, MAX(model_type) AS model_type,
			COUNT(*) AS call_count,
			SUM(CASE WHEN success THEN 1 ELSE 0 END) AS successful_calls,
			SUM(CASE WHEN success THEN 0 ELSE 1 END) AS failed_calls,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
			COALESCE(SUM(cache_miss_tokens), 0) AS cache_miss_tokens,
			SUM(CASE WHEN cache_reported THEN 1 ELSE 0 END) AS cache_reported_calls,
			SUM(CASE WHEN cache_reported AND cache_read_tokens > 0 THEN 1 ELSE 0 END) AS cache_hit_calls,
			COALESCE(SUM(duration_ms), 0) AS model_duration_ms,
			SUM(CASE WHEN pricing_configured THEN 1 ELSE 0 END) AS priced_calls,
			SUM(CASE WHEN pricing_configured THEN 0 ELSE 1 END) AS unpriced_calls`).
		Group("model_id").
		Order("model_name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	stats := make([]types.ModelUsageStat, 0, len(rows))
	indexByModelID := make(map[string]int, len(rows))
	for _, row := range rows {
		stats = append(stats, types.ModelUsageStat{
			ModelID: row.ModelID, ModelName: row.ModelName, ModelType: row.ModelType,
			Usage: usageFromAggregateRow(row),
		})
		indexByModelID[row.ModelID] = len(stats) - 1
	}

	var costs []modelCostAggregateRow
	costQuery := e.db.WithContext(ctx).Model(&evaluationModelCallRecord{}).
		Where("tenant_id = ? AND pricing_configured = ?", tenantID, true)
	if startTime != nil {
		costQuery = costQuery.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		costQuery = costQuery.Where("created_at <= ?", *endTime)
	}
	if err := costQuery.
		Select("model_id, currency, COALESCE(SUM(estimated_cost), 0) AS cost").
		Group("model_id, currency").
		Scan(&costs).Error; err != nil {
		return nil, err
	}
	for _, cost := range costs {
		if index, ok := indexByModelID[cost.ModelID]; ok {
			stats[index].Usage.CostByCurrency[cost.Currency] = cost.Cost
		}
	}

	// The overall cards remain cheap to scan, while this second aggregate gives
	// the UI an optional purpose-level drill-down without exposing prompts.
	var purposeRows []modelUsageAggregateRow
	purposeQuery := e.db.WithContext(ctx).Model(&evaluationModelCallRecord{}).
		Where("tenant_id = ?", tenantID)
	if startTime != nil {
		purposeQuery = purposeQuery.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		purposeQuery = purposeQuery.Where("created_at <= ?", *endTime)
	}
	if err := purposeQuery.
		Select(`model_id, purpose,
			COUNT(*) AS call_count,
			SUM(CASE WHEN success THEN 1 ELSE 0 END) AS successful_calls,
			SUM(CASE WHEN success THEN 0 ELSE 1 END) AS failed_calls,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens,
			COALESCE(SUM(cache_miss_tokens), 0) AS cache_miss_tokens,
			SUM(CASE WHEN cache_reported THEN 1 ELSE 0 END) AS cache_reported_calls,
			SUM(CASE WHEN cache_reported AND cache_read_tokens > 0 THEN 1 ELSE 0 END) AS cache_hit_calls,
			COALESCE(SUM(duration_ms), 0) AS model_duration_ms,
			SUM(CASE WHEN pricing_configured THEN 1 ELSE 0 END) AS priced_calls,
			SUM(CASE WHEN pricing_configured THEN 0 ELSE 1 END) AS unpriced_calls`).
		Group("model_id, purpose").
		Order("model_id ASC, call_count DESC, purpose ASC").
		Scan(&purposeRows).Error; err != nil {
		return nil, err
	}
	purposeIndex := make(map[string][2]int, len(purposeRows))
	for _, row := range purposeRows {
		modelIndex, ok := indexByModelID[row.ModelID]
		if !ok {
			continue
		}
		stats[modelIndex].Purposes = append(stats[modelIndex].Purposes, types.ModelPurposeUsageStat{
			Purpose: row.Purpose, Usage: usageFromAggregateRow(row),
		})
		purposeIndex[row.ModelID+"\x00"+row.Purpose] = [2]int{modelIndex, len(stats[modelIndex].Purposes) - 1}
	}

	var purposeCosts []modelCostAggregateRow
	purposeCostQuery := e.db.WithContext(ctx).Model(&evaluationModelCallRecord{}).
		Where("tenant_id = ? AND pricing_configured = ?", tenantID, true)
	if startTime != nil {
		purposeCostQuery = purposeCostQuery.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		purposeCostQuery = purposeCostQuery.Where("created_at <= ?", *endTime)
	}
	if err := purposeCostQuery.
		Select("model_id, purpose, currency, COALESCE(SUM(estimated_cost), 0) AS cost").
		Group("model_id, purpose, currency").
		Scan(&purposeCosts).Error; err != nil {
		return nil, err
	}
	for _, cost := range purposeCosts {
		if indexes, ok := purposeIndex[cost.ModelID+"\x00"+cost.Purpose]; ok {
			stats[indexes[0]].Purposes[indexes[1]].Usage.CostByCurrency[cost.Currency] = cost.Cost
		}
	}

	var cacheRows []embeddingCacheAggregateRow
	cacheQuery := e.db.WithContext(ctx).Model(&embeddingCacheUsageRecord{}).
		Where("tenant_id = ?", tenantID)
	if startTime != nil {
		cacheQuery = cacheQuery.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		cacheQuery = cacheQuery.Where("created_at <= ?", *endTime)
	}
	if err := cacheQuery.
		Select(`model_id, MAX(model_name) AS model_name,
			COALESCE(SUM(lookup_count), 0) AS lookup_count,
			COALESCE(SUM(hit_count), 0) AS hit_count,
			COALESCE(SUM(miss_count), 0) AS miss_count,
			COALESCE(SUM(deduplicated_count), 0) AS deduplicated_count,
			COALESCE(SUM(avoided_computations), 0) AS avoided_computations`).
		Group("model_id").
		Scan(&cacheRows).Error; err != nil {
		return nil, err
	}
	for _, row := range cacheRows {
		modelIndex, ok := indexByModelID[row.ModelID]
		if !ok {
			stats = append(stats, types.ModelUsageStat{
				ModelID: row.ModelID, ModelName: row.ModelName, ModelType: types.ModelTypeEmbedding,
				Usage: types.EvaluationUsage{CostByCurrency: make(map[string]float64)},
			})
			modelIndex = len(stats) - 1
			indexByModelID[row.ModelID] = modelIndex
		}
		cacheUsage := &types.EmbeddingCacheUsageStat{
			LookupCount: row.LookupCount, HitCount: row.HitCount, MissCount: row.MissCount,
			DeduplicatedCount: row.DeduplicatedCount, AvoidedComputations: row.AvoidedComputations,
		}
		if row.LookupCount > 0 {
			cacheUsage.HitRate = float64(row.HitCount) / float64(row.LookupCount)
			cacheUsage.AvoidedRate = float64(row.AvoidedComputations) / float64(row.LookupCount)
		}
		stats[modelIndex].EmbeddingCache = cacheUsage
	}
	return stats, nil
}
