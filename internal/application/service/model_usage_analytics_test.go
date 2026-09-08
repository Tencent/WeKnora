package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type modelUsageAnalyticsRepositoryStub struct {
	tenantID uint64
	query    types.ModelUsageAnalyticsQuery
	result   *types.ModelUsageAnalyticsResult
}

func (s *modelUsageAnalyticsRepositoryStub) Create(context.Context, *types.ModelUsage) error {
	return nil
}

func (s *modelUsageAnalyticsRepositoryStub) GetByID(context.Context, uint64, string) (*types.ModelUsage, error) {
	return nil, nil
}

func (s *modelUsageAnalyticsRepositoryStub) AggregateEvaluationRun(
	context.Context, uint64, string,
) (*types.EvaluationModelUsageAggregate, error) {
	return nil, nil
}

func (s *modelUsageAnalyticsRepositoryStub) AggregateAnalytics(
	_ context.Context, tenantID uint64, query types.ModelUsageAnalyticsQuery,
) (*types.ModelUsageAnalyticsResult, error) {
	s.tenantID, s.query = tenantID, query
	return s.result, nil
}

func TestModelUsageAnalyticsServiceUsesAuthenticatedTenantContext(t *testing.T) {
	repo := &modelUsageAnalyticsRepositoryStub{result: &types.ModelUsageAnalyticsResult{}}
	service := NewModelUsageAnalyticsService(repo)
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	query := types.ModelUsageAnalyticsQuery{
		ModelID: "model-a", StartTime: start, EndTime: start.Add(time.Hour),
		Interval: types.ModelUsageAnalyticsIntervalHour,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	_, err := service.GetAnalytics(ctx, query)
	require.NoError(t, err)
	require.Equal(t, uint64(42), repo.tenantID)
	require.Equal(t, query, repo.query)
}
