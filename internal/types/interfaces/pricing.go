package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// PricingRepository persists pricing rules and derived model usage costs.
type PricingRepository interface {
	CreatePricing(ctx context.Context, rule *types.ModelPricing) error
	ImportPricingBatch(ctx context.Context, rules []types.PricingImportRule) (*types.PricingImportResult, error)
	ResolvePricing(
		ctx context.Context, provider, modelName string, callType types.CallType, at time.Time,
	) (*types.ModelPricing, error)
	CreateCost(ctx context.Context, cost *types.ModelUsageCost) error
	GetCostByUsageID(ctx context.Context, tenantID uint64, usageID string) (*types.ModelUsageCost, error)
}
