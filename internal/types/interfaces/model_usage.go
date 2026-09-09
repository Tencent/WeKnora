package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type ModelUsageQuery struct {
	Model string
	Start *time.Time
	End   *time.Time
}

type ModelUsageRepository interface {
	Create(ctx context.Context, usage *types.ModelUsage) error
	Summary(ctx context.Context, tenantID uint64, query ModelUsageQuery) (*types.ModelUsageSummary, error)
}
