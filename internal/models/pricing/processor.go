package pricing

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type Processor struct {
	repo       interfaces.PricingRepository
	calculator *Calculator
}

func NewProcessor(repo interfaces.PricingRepository) *Processor {
	return &Processor{repo: repo, calculator: NewCalculator()}
}

func (p *Processor) Process(ctx context.Context, usage *types.ModelUsage) error {
	if usage == nil {
		return nil
	}
	// A cost row is only persisted when both the resolved identity and the call
	// time are known AND a pricing rule actually resolves. Otherwise the usage
	// is left without a cost row so it can be re-processed later (backfill)
	// once pricing data becomes available, without tripping the usage_id
	// UNIQUE constraint on model_usage_cost.
	if usage.ResolvedProvider == "" || usage.ResolvedModelName == nil || usage.StartedAt == nil {
		return nil
	}
	rule, err := p.repo.ResolvePricing(
		ctx, usage.ResolvedProvider, *usage.ResolvedModelName, usage.CallType, *usage.StartedAt,
	)
	if err != nil {
		return err
	}
	if rule == nil {
		return nil
	}
	cost, err := p.calculator.Calculate(usage, rule)
	if err != nil {
		return err
	}
	if cost.CalculatedAt.IsZero() {
		cost.CalculatedAt = time.Now().UTC()
	}
	return p.repo.CreateCost(ctx, cost)
}
