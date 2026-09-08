package repository

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type pricingRepository struct{ db *gorm.DB }

// NewPricingRepository creates a database-backed pricing repository.
func NewPricingRepository(db *gorm.DB) interfaces.PricingRepository {
	return &pricingRepository{db: db}
}

func (r *pricingRepository) CreatePricing(ctx context.Context, rule *types.ModelPricing) error {
	if rule == nil {
		return fmt.Errorf("model_pricing: rule is nil")
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		q := tx.Model(&types.ModelPricing{}).
			Where(
				"resolved_provider = ? AND resolved_model_name = ? AND call_type = ?",
				rule.ResolvedProvider, rule.ResolvedModelName, rule.CallType,
			).
			Where("effective_to IS NULL OR effective_to > ?", rule.EffectiveFrom)
		if rule.EffectiveTo != nil {
			q = q.Where("effective_from < ?", *rule.EffectiveTo)
		}
		if err := q.Count(&count).Error; err != nil {
			return fmt.Errorf("check model_pricing overlap: %w", err)
		}
		if count != 0 {
			return fmt.Errorf("model_pricing: overlapping effective interval")
		}
		return tx.Create(rule).Error
	})
}

func (r *pricingRepository) ImportPricingBatch(
	ctx context.Context, rules []types.PricingImportRule,
) (*types.PricingImportResult, error) {
	seen := make(map[string]struct{}, len(rules))
	ids := make([]string, 0, len(rules)*2)
	for i := range rules {
		rule := &rules[i]
		if rule.Pricing.ID == "" {
			return nil, fmt.Errorf("model_pricing import rule %d: stable id is required", i)
		}
		if err := validateCanonicalPricingUUID("stable id", rule.Pricing.ID); err != nil {
			return nil, fmt.Errorf("model_pricing import rule %q: %w", rule.Pricing.ID, err)
		}
		if _, duplicate := seen[rule.Pricing.ID]; duplicate {
			return nil, fmt.Errorf("model_pricing import: duplicate rule id %q", rule.Pricing.ID)
		}
		seen[rule.Pricing.ID] = struct{}{}
		if err := rule.Pricing.Validate(); err != nil {
			return nil, fmt.Errorf("model_pricing import rule %q: %w", rule.Pricing.ID, err)
		}
		if strings.TrimSpace(rule.Pricing.Currency) == "" {
			return nil, fmt.Errorf(
				"model_pricing import rule %q: currency must not be empty or whitespace",
				rule.Pricing.ID,
			)
		}
		ids = append(ids, rule.Pricing.ID)
		if rule.ClosesRuleID != nil {
			if *rule.ClosesRuleID == "" || *rule.ClosesRuleID == rule.Pricing.ID {
				return nil, fmt.Errorf("model_pricing import rule %q: invalid closes_rule_id", rule.Pricing.ID)
			}
			if err := validateCanonicalPricingUUID("closes_rule_id", *rule.ClosesRuleID); err != nil {
				return nil, fmt.Errorf("model_pricing import rule %q: %w", rule.Pricing.ID, err)
			}
			ids = append(ids, *rule.ClosesRuleID)
		}
	}

	result := &types.PricingImportResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var persisted []types.ModelPricing
		if len(ids) > 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id IN ?", ids).Find(&persisted).Error; err != nil {
				return fmt.Errorf("inspect existing model_pricing rules: %w", err)
			}
		}
		existing := make(map[string]*types.ModelPricing, len(persisted))
		for i := range persisted {
			existing[persisted[i].ID] = &persisted[i]
		}

		for i := range rules {
			incoming := &rules[i].Pricing
			if current := existing[incoming.ID]; current != nil {
				if !samePricingSemantics(current, incoming) {
					return fmt.Errorf(
						"model_pricing import: rule id %q already exists with different semantic content",
						incoming.ID,
					)
				}
				result.NoOp++
			}
		}

		// Close old intervals before inserting their replacements so the
		// database overlap trigger observes the intended half-open intervals.
		for i := range rules {
			instruction := &rules[i]
			if instruction.ClosesRuleID == nil {
				continue
			}
			incoming := &instruction.Pricing
			old := existing[*instruction.ClosesRuleID]
			if old == nil {
				return fmt.Errorf(
					"model_pricing import rule %q: closes_rule_id %q does not exist",
					incoming.ID, *instruction.ClosesRuleID,
				)
			}
			if old.ResolvedProvider != incoming.ResolvedProvider ||
				old.ResolvedModelName != incoming.ResolvedModelName || old.CallType != incoming.CallType {
				return fmt.Errorf(
					"model_pricing import rule %q: closes_rule_id %q has different runtime identity",
					incoming.ID, old.ID,
				)
			}
			if !old.EffectiveFrom.Before(incoming.EffectiveFrom) {
				return fmt.Errorf(
					"model_pricing import rule %q: replacement must start after closes_rule_id %q",
					incoming.ID, old.ID,
				)
			}
			switch {
			case old.EffectiveTo == nil:
				res := tx.Model(&types.ModelPricing{}).Where("id = ? AND effective_to IS NULL", old.ID).
					Update("effective_to", incoming.EffectiveFrom)
				if res.Error != nil {
					return fmt.Errorf("close model_pricing rule %q: %w", old.ID, res.Error)
				}
				if res.RowsAffected != 1 {
					return fmt.Errorf("close model_pricing rule %q: interval changed concurrently", old.ID)
				}
				closedAt := incoming.EffectiveFrom
				old.EffectiveTo = &closedAt
				result.Closed++
			case old.EffectiveTo.Equal(incoming.EffectiveFrom):
				if existing[incoming.ID] == nil {
					return fmt.Errorf(
						"model_pricing import rule %q: closes_rule_id %q is already closed "+
							"but replacement does not exist",
						incoming.ID, old.ID,
					)
				}
			default:
				return fmt.Errorf(
					"model_pricing import rule %q: closes_rule_id %q was already closed at a different time",
					incoming.ID, old.ID,
				)
			}
		}

		for i := range rules {
			incoming := &rules[i].Pricing
			if existing[incoming.ID] != nil {
				continue
			}
			if err := tx.Create(incoming).Error; err != nil {
				return fmt.Errorf("insert model_pricing rule %q: %w", incoming.ID, err)
			}
			result.Inserted++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func samePricingSemantics(a, b *types.ModelPricing) bool {
	return a.ID == b.ID &&
		a.ResolvedProvider == b.ResolvedProvider && a.ResolvedModelName == b.ResolvedModelName &&
		a.CallType == b.CallType && a.Currency == b.Currency && a.BillingMode == b.BillingMode &&
		decimalPtrEqual(a.InputTokenPrice, b.InputTokenPrice) &&
		decimalPtrEqual(a.OutputTokenPrice, b.OutputTokenPrice) &&
		decimalPtrEqual(a.TotalTokenPrice, b.TotalTokenPrice) &&
		decimalPtrEqual(a.CacheReadTokenPrice, b.CacheReadTokenPrice) &&
		decimalPtrEqual(a.CacheWriteTokenPrice, b.CacheWriteTokenPrice) &&
		decimalPtrEqual(a.PerRequestPrice, b.PerRequestPrice) &&
		decimalPtrEqual(a.PerInputPrice, b.PerInputPrice) &&
		decimalPtrEqual(a.PerPairPrice, b.PerPairPrice) &&
		decimalEqual(a.UnitScale, b.UnitScale) && a.EffectiveFrom.Equal(b.EffectiveFrom) &&
		effectiveToReplayCompatible(a.EffectiveTo, b.EffectiveTo) && a.PricingVersion == b.PricingVersion &&
		a.SourceName == b.SourceName && stringPtrEqual(a.SourceReference, b.SourceReference) &&
		timePtrEqual(a.SourceRetrievedAt, b.SourceRetrievedAt)
}

// A later closes_rule_id import is allowed to narrow an open historical rule.
// Replaying that original source (incoming effective_to NULL) is still a no-op;
// every other effective_to difference remains a semantic conflict.
func effectiveToReplayCompatible(persisted, incoming *time.Time) bool {
	if timePtrEqual(persisted, incoming) {
		return true
	}
	return persisted != nil && incoming == nil
}

func validateCanonicalPricingUUID(name, raw string) error {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s must be a canonical lowercase UUID: %w", name, err)
	}
	if parsed.String() != raw {
		return fmt.Errorf("%s must be a canonical lowercase UUID", name)
	}
	return nil
}

func decimalEqual(a, b types.Decimal) bool {
	ar, aOK := new(big.Rat).SetString(string(a))
	br, bOK := new(big.Rat).SetString(string(b))
	return aOK && bOK && ar.Cmp(br) == 0
}

func decimalPtrEqual(a, b *types.Decimal) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return decimalEqual(*a, *b)
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (r *pricingRepository) ResolvePricing(
	ctx context.Context, provider, modelName string, callType types.CallType, at time.Time,
) (*types.ModelPricing, error) {
	if provider == "" || modelName == "" || at.IsZero() {
		return nil, nil
	}
	var rules []types.ModelPricing
	err := r.db.WithContext(ctx).
		Where("resolved_provider = ? AND resolved_model_name = ? AND call_type = ?", provider, modelName, callType).
		Where("effective_from <= ?", at).
		Where("effective_to IS NULL OR effective_to > ?", at).
		Order("effective_from DESC").Limit(2).Find(&rules).Error
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}
	if len(rules) > 1 {
		return nil, fmt.Errorf("model_pricing: overlapping rules found during resolution")
	}
	return &rules[0], nil
}

func (r *pricingRepository) CreateCost(ctx context.Context, cost *types.ModelUsageCost) error {
	if cost == nil || cost.UsageID == "" {
		return fmt.Errorf("model_usage_cost: usage_id is required")
	}
	return r.db.WithContext(ctx).Create(cost).Error
}

func (r *pricingRepository) GetCostByUsageID(
	ctx context.Context, tenantID uint64, usageID string,
) (*types.ModelUsageCost, error) {
	var cost types.ModelUsageCost
	// Scope the read through model_usage so a caller can only see a cost for a
	// usage that belongs to its own tenant. A cross-tenant usage_id resolves to
	// not-found (nil, nil) exactly like a non-existent one, without leaking the
	// existence of another tenant's usage.
	err := r.db.WithContext(ctx).
		Table("model_usage_cost").
		Select("model_usage_cost.*").
		Joins("JOIN model_usage ON model_usage_cost.usage_id = model_usage.id").
		Where("model_usage.tenant_id = ? AND model_usage_cost.usage_id = ?", tenantID, usageID).
		First(&cost).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cost, nil
}
