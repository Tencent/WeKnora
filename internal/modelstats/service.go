// Package modelstats validates model usage queries and immutable price operations.
package modelstats

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultWindow = 30 * 24 * time.Hour
	maximumWindow = 366 * 24 * time.Hour
)

// Store supplies database-native aggregates and immutable price versions.
type Store interface {
	QueryModelUsage(context.Context, types.ModelUsageQuery) ([]types.ModelUsageStatistics, error)
	CreateModelPrice(context.Context, *types.ModelPriceVersion) error
	ListModelPrices(context.Context, uint64, string) ([]*types.ModelPriceVersion, error)
}

// Service validates tenant-scoped statistics and pricing operations.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService creates a tenant-scoped model statistics service.
func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

// Usage returns bounded model usage aggregates for a UTC interval.
func (s *Service) Usage(
	ctx context.Context,
	tenantID uint64,
	modelIDs []string,
	from, to *time.Time,
) (*types.ModelUsageResponse, error) {
	if s == nil || s.store == nil || tenantID == 0 {
		return nil, errors.New("model usage: tenant and store are required")
	}
	end := s.now().UTC()
	if to != nil {
		end = to.UTC()
	}
	start := end.Add(-defaultWindow)
	if from != nil {
		start = from.UTC()
	}
	if !start.Before(end) {
		return nil, errors.New("model usage: from must be before to")
	}
	if end.Sub(start) > maximumWindow {
		return nil, errors.New("model usage: interval must not exceed 366 days")
	}
	ids := normalizeModelIDs(modelIDs)
	items, err := s.store.QueryModelUsage(ctx, types.ModelUsageQuery{
		TenantID: tenantID, ModelIDs: ids, From: start, To: end,
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModelID < items[j].ModelID })
	if len(items) == 0 {
		items = []types.ModelUsageStatistics{}
	}
	return &types.ModelUsageResponse{From: start, To: end, Items: items}, nil
}

// Prices lists immutable price versions for one tenant model.
func (s *Service) Prices(
	ctx context.Context,
	tenantID uint64,
	modelID string,
) ([]*types.ModelPriceVersion, error) {
	if s == nil || s.store == nil || tenantID == 0 || strings.TrimSpace(modelID) == "" {
		return nil, errors.New("model prices: tenant, model, and store are required")
	}
	return s.store.ListModelPrices(ctx, tenantID, strings.TrimSpace(modelID))
}

// PutPrice creates one immutable price version after assigning its tenant and model.
func (s *Service) PutPrice(
	ctx context.Context,
	tenantID uint64,
	modelID string,
	price *types.ModelPriceVersion,
) error {
	if s == nil || s.store == nil || tenantID == 0 || strings.TrimSpace(modelID) == "" || price == nil {
		return errors.New("put model price: tenant, model, price, and store are required")
	}
	price.TenantID = tenantID
	price.ModelID = strings.TrimSpace(modelID)
	price.ID = ""
	price.CreatedAt = time.Time{}
	return s.store.CreateModelPrice(ctx, price)
}

func normalizeModelIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
