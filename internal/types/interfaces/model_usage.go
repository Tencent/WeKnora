package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ModelUsageRepository persists model usage events and builds aggregated
// reports for the usage dashboard.
type ModelUsageRepository interface {
	Create(ctx context.Context, event *types.ModelUsageEvent) error
	Report(ctx context.Context, tenantID uint64, query types.ModelUsageQuery) (*types.ModelUsageReport, error)
}

// ModelUsageService resolves the reporting window and returns the tenant's
// usage report.
type ModelUsageService interface {
	GetUsageReport(ctx context.Context, query types.ModelUsageQuery) (*types.ModelUsageReport, error)
}
