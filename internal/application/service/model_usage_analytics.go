package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type modelUsageAnalyticsService struct {
	modelUsage interfaces.ModelUsageRepository
}

// NewModelUsageAnalyticsService creates a tenant-scoped model usage analytics service.
func NewModelUsageAnalyticsService(modelUsage interfaces.ModelUsageRepository) interfaces.ModelUsageAnalyticsService {
	return &modelUsageAnalyticsService{modelUsage: modelUsage}
}

func (s *modelUsageAnalyticsService) GetAnalytics(
	ctx context.Context,
	query types.ModelUsageAnalyticsQuery,
) (*types.ModelUsageAnalyticsResult, error) {
	if !query.StartTime.Before(query.EndTime) {
		return nil, fmt.Errorf("model usage analytics: start_time must be before end_time")
	}
	switch query.Interval {
	case types.ModelUsageAnalyticsIntervalHour, types.ModelUsageAnalyticsIntervalDay:
	default:
		return nil, fmt.Errorf("model usage analytics: unsupported interval %q", query.Interval)
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	return s.modelUsage.AggregateAnalytics(ctx, tenantID, query)
}
