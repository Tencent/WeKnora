package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// modelUsageService resolves the reporting window for the usage dashboard and
// delegates aggregation to the repository.
type modelUsageService struct {
	repo interfaces.ModelUsageRepository
}

// modelUsageTimelineBucket is fixed regardless of range: the stacked-bar
// chart always renders 2-hour buckets (the repository additionally hides
// buckets with too little traffic).
const modelUsageTimelineBucket = 2 * time.Hour

// NewModelUsageService creates the model usage report service.
func NewModelUsageService(repo interfaces.ModelUsageRepository) interfaces.ModelUsageService {
	return &modelUsageService{repo: repo}
}

func (s *modelUsageService) GetUsageReport(
	ctx context.Context,
	query types.ModelUsageQuery,
) (*types.ModelUsageReport, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, fmt.Errorf("tenant ID not found in context")
	}

	now := time.Now().UTC()
	query.End = now
	query.Start, query.BucketSize = usageWindow(query.Range, now)
	query.RecentLimit = 30

	if query.ModelType == types.ModelType("all") {
		query.ModelType = ""
	}
	return s.repo.Report(ctx, tenantID, query)
}

func usageWindow(value string, now time.Time) (time.Time, time.Duration) {
	switch value {
	case "15m":
		return now.Add(-15 * time.Minute), modelUsageTimelineBucket
	case "1h":
		return now.Add(-time.Hour), modelUsageTimelineBucket
	case "7d":
		return now.Add(-7 * 24 * time.Hour), modelUsageTimelineBucket
	default:
		return now.Add(-24 * time.Hour), modelUsageTimelineBucket
	}
}
