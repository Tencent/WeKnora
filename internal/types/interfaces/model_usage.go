package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ModelUsageRepository persists call-level model usage rows. Every read is
// tenant-scoped at the query boundary; the append-only ledger is never read
// back without a tenant filter.
type ModelUsageRepository interface {
	Create(ctx context.Context, usage *types.ModelUsage) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.ModelUsage, error)
	AggregateEvaluationRun(
		ctx context.Context, tenantID uint64, evaluationRunID string,
	) (*types.EvaluationModelUsageAggregate, error)
	AggregateAnalytics(
		ctx context.Context, tenantID uint64, query types.ModelUsageAnalyticsQuery,
	) (*types.ModelUsageAnalyticsResult, error)
}

// ModelUsageAnalyticsService aggregates model usage for the authenticated tenant.
type ModelUsageAnalyticsService interface {
	GetAnalytics(ctx context.Context, query types.ModelUsageAnalyticsQuery) (*types.ModelUsageAnalyticsResult, error)
}
