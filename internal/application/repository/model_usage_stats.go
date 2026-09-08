package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type modelUsageRepository struct{ db *gorm.DB }

func NewModelUsageRepository(db *gorm.DB) interfaces.ModelUsageRepository {
	return &modelUsageRepository{db: db}
}

func (r *modelUsageRepository) Create(ctx context.Context, usage *types.ModelUsage) error {
	return r.db.WithContext(ctx).Create(usage).Error
}

func (r *modelUsageRepository) Summary(ctx context.Context, tenantID uint64, query interfaces.ModelUsageQuery) (*types.ModelUsageSummary, error) {
	tx := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if query.Model != "" {
		tx = tx.Where("model = ?", query.Model)
	}
	if query.Start != nil {
		tx = tx.Where("created_at >= ?", *query.Start)
	}
	if query.End != nil {
		tx = tx.Where("created_at < ?", *query.End)
	}
	var rows []*types.ModelUsage
	if err := tx.Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := &types.ModelUsageSummary{}
	var cacheDenominator int64
	var providerCacheEligibleCalls int64
	for _, row := range rows {
		result.Calls += int64(row.ActualCalls)
		result.PromptTokens += int64(row.PromptTokens)
		result.CompletionTokens += int64(row.CompletionTokens)
		result.TotalTokens += int64(row.TotalTokens)
		if row.CallType == "embedding" {
			result.EmbeddingInputCount += int64(row.InputCount)
			result.EmbeddingCacheHits += int64(row.LocalCacheHits)
		}
		result.CacheReadTokens += int64(row.CacheReadTokens)
		result.CacheWriteTokens += int64(row.CacheWriteTokens)
		if row.CallType == "chat" {
			providerCacheEligibleCalls += int64(row.ActualCalls)
			if row.CacheReported {
				result.CacheReportedCalls++
				cacheDenominator += int64(row.PromptTokens)
			}
		}
		if row.CostCNY != nil {
			result.CostKnownCalls++
			result.EstimatedCostCNY += *row.CostCNY
		}
		if row.Status == "failed" {
			result.FailedCalls += int64(row.ActualCalls)
		} else {
			result.SuccessfulCalls += int64(row.ActualCalls)
		}
	}
	if providerCacheEligibleCalls > 0 {
		result.CacheReportingRate = float64(result.CacheReportedCalls) / float64(providerCacheEligibleCalls)
	}
	if cacheDenominator > 0 {
		rate := float64(result.CacheReadTokens) / float64(cacheDenominator)
		result.ProviderCacheHitRate = &rate
	}
	if result.EmbeddingInputCount > 0 {
		rate := float64(result.EmbeddingCacheHits) / float64(result.EmbeddingInputCount)
		result.EmbeddingCacheHitRate = &rate
	}
	return result, nil
}
