package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/modelobs"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ModelObservabilityRepository persists call accounting and versioned model prices.
type ModelObservabilityRepository struct{ db *gorm.DB }

type modelPriceScopeLock struct {
	mutex sync.Mutex
	users int
}

var modelPriceScopeLocks = struct {
	sync.Mutex
	values map[string]*modelPriceScopeLock
}{values: make(map[string]*modelPriceScopeLock)}

// NewModelObservabilityRepository creates the model call ledger and pricing store.
func NewModelObservabilityRepository(db *gorm.DB) *ModelObservabilityRepository {
	return &ModelObservabilityRepository{db: db}
}

type evaluationCostCountRow struct {
	CallCount               int64
	AccountingCompleteCalls int64
	UnpricedCalls           int64
	UsageUnreportedCalls    int64
	StartedCalls            int64
}

type evaluationCostTotalRow struct {
	Currency       string
	CostMicrounits int64
}

// EvaluationCost returns task-scoped cost completeness and currency totals.
func (r *ModelObservabilityRepository) EvaluationCost(
	ctx context.Context,
	tenantID uint64,
	taskID string,
) (*types.EvaluationRuntimeCost, error) {
	base := r.db.WithContext(ctx).Table("model_call_records").
		Where("tenant_id = ? AND evaluation_task_id = ? AND deleted_at IS NULL", tenantID, taskID)

	var counts evaluationCostCountRow
	if err := base.Select(`
		COUNT(*) AS call_count,
		COALESCE(SUM(CASE WHEN accounting_complete THEN 1 ELSE 0 END), 0) AS accounting_complete_calls,
		COALESCE(SUM(CASE WHEN status <> 'started' AND total_tokens IS NOT NULL
			AND cost_microunits IS NULL THEN 1 ELSE 0 END), 0) AS unpriced_calls,
		COALESCE(SUM(CASE WHEN status <> 'started' AND total_tokens IS NULL
			THEN 1 ELSE 0 END), 0) AS usage_unreported_calls,
		COALESCE(SUM(CASE WHEN status = 'started' THEN 1 ELSE 0 END), 0) AS started_calls`).
		Scan(&counts).Error; err != nil {
		return nil, fmt.Errorf("query evaluation cost counts: %w", err)
	}

	var rows []evaluationCostTotalRow
	if err := base.Select("currency, SUM(cost_microunits) AS cost_microunits").
		Where("cost_microunits IS NOT NULL AND currency <> ''").
		Group("currency").Order("currency").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query evaluation cost totals: %w", err)
	}
	totals := make([]types.ModelCostTotal, 0, len(rows))
	for _, row := range rows {
		totals = append(totals, types.ModelCostTotal{
			Currency: row.Currency, CostMicrounits: row.CostMicrounits,
		})
	}
	return &types.EvaluationRuntimeCost{
		CallCount: counts.CallCount, AccountingCompleteCalls: counts.AccountingCompleteCalls,
		UnpricedCalls: counts.UnpricedCalls, UsageUnreportedCalls: counts.UsageUnreportedCalls,
		StartedCalls: counts.StartedCalls, Totals: totals,
	}, nil
}

// StartModelCall persists the initial call record after validating its identity.
func (r *ModelObservabilityRepository) StartModelCall(ctx context.Context, record *types.ModelCallRecord) error {
	if record == nil || record.ID == "" || record.TenantID == 0 || record.ModelID == "" ||
		record.Purpose == "" || record.Operation == "" || record.StartedAt.IsZero() {
		return errors.New("start model call: id, tenant, model, purpose, operation, and started_at are required")
	}
	if record.Status != types.ModelCallStatusStarted {
		return errors.New("start model call: status must be started")
	}
	if err := validateEvaluationTaskJSONObject(record.ModelSnapshot, false); err != nil {
		return fmt.Errorf("start model call: model_snapshot: %w", err)
	}
	record.StartedAt = record.StartedAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("start model call %s: %w", record.ID, err)
	}
	return nil
}

// CompleteModelCall atomically records one terminal accounting result.
func (r *ModelObservabilityRepository) CompleteModelCall(
	ctx context.Context,
	completion types.ModelCallCompletion,
) error {
	if completion.ID == "" || completion.EndedAt.IsZero() || completion.DurationMs < 0 {
		return errors.New("complete model call: id, ended_at, and non-negative duration are required")
	}
	switch completion.Status {
	case types.ModelCallStatusSuccess, types.ModelCallStatusError, types.ModelCallStatusCanceled:
	default:
		return errors.New("complete model call: unsupported terminal status")
	}
	endedAt := completion.EndedAt.UTC()
	var snapshot any = gorm.Expr("model_snapshot")
	if len(completion.ModelSnapshot) > 0 {
		if err := validateEvaluationTaskJSONObject(completion.ModelSnapshot, false); err != nil {
			return fmt.Errorf("complete model call: model_snapshot: %w", err)
		}
		var initial types.ModelCallRecord
		if err := r.db.WithContext(
			ctx,
		).Select(
			"model_snapshot",
		).First(
			&initial,
			"id = ?",
			completion.ID,
		).Error; err !=
			nil {
			return fmt.Errorf("complete model call: load frozen snapshot: %w", err)
		}
		var frozen, next map[string]any
		if json.Unmarshal(
			initial.ModelSnapshot,
			&frozen,
		) !=
			nil ||
			json.Unmarshal(
				completion.ModelSnapshot,
				&next,
			) !=
				nil {
			return errors.New("complete model call: invalid frozen snapshot")
		}
		delete(frozen, "billing_usage")
		delete(next, "billing_usage")
		if !reflect.DeepEqual(frozen, next) {
			return errors.New("complete model call: frozen identity or price conflict")
		}
		snapshot = completion.ModelSnapshot
	}
	result := r.db.WithContext(ctx).Model(&types.ModelCallRecord{}).
		Where("id = ? AND status = ?", completion.ID, types.ModelCallStatusStarted).
		Updates(map[string]any{
			"model_snapshot":              snapshot,
			"ended_at":                    endedAt,
			"duration_ms":                 completion.DurationMs,
			"status":                      completion.Status,
			"error_code":                  completion.ErrorCode,
			"prompt_tokens":               completion.PromptTokens,
			"completion_tokens":           completion.CompletionTokens,
			"total_tokens":                completion.TotalTokens,
			"provider_cache_status":       completion.ProviderCacheStatus,
			"provider_cache_read_tokens":  completion.ProviderCacheReadTokens,
			"provider_cache_write_tokens": completion.ProviderCacheWriteTokens,
			"provider_cache_miss_tokens":  completion.ProviderCacheMissTokens,
			"application_cache_status":    completion.ApplicationCacheStatus,
			"cost_microunits":             completion.CostMicrounits,
			"accounting_complete":         completion.AccountingComplete,
			"updated_at":                  endedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("complete model call %s: %w", completion.ID, result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if result.RowsAffected > 1 {
		return fmt.Errorf("complete model call %s: invariant violation", completion.ID)
	}
	var existing types.ModelCallRecord
	if err := r.db.WithContext(ctx).First(&existing, "id = ?", completion.ID).Error; err != nil {
		return fmt.Errorf("complete model call %s: %w", completion.ID, err)
	}
	if existing.Status == types.ModelCallStatusStarted {
		return fmt.Errorf("complete model call %s: concurrent state conflict", completion.ID)
	}
	if existing.EndedAt == nil || existing.DurationMs == nil {
		return errors.New("complete model call: incomplete terminal payload")
	}
	expected := types.ModelCallCompletion{
		ID:                       existing.ID,
		EndedAt:                  *existing.EndedAt,
		DurationMs:               *existing.DurationMs,
		Status:                   existing.Status,
		ErrorCode:                existing.ErrorCode,
		PromptTokens:             existing.PromptTokens,
		CompletionTokens:         existing.CompletionTokens,
		TotalTokens:              existing.TotalTokens,
		ProviderCacheStatus:      existing.ProviderCacheStatus,
		ProviderCacheReadTokens:  existing.ProviderCacheReadTokens,
		ProviderCacheWriteTokens: existing.ProviderCacheWriteTokens,
		ProviderCacheMissTokens:  existing.ProviderCacheMissTokens,
		ApplicationCacheStatus:   existing.ApplicationCacheStatus,
		CostMicrounits:           existing.CostMicrounits,
		AccountingComplete:       existing.AccountingComplete,
	}
	// Database timestamp precision is microseconds on PostgreSQL.
	if !expected.EndedAt.Truncate(time.Microsecond).Equal(completion.EndedAt.Truncate(time.Microsecond)) {
		return errors.New("complete model call: terminal payload conflict")
	}
	if len(completion.ModelSnapshot) > 0 {
		var have, want any
		if json.Unmarshal(
			existing.ModelSnapshot,
			&have,
		) !=
			nil ||
			json.Unmarshal(
				completion.ModelSnapshot,
				&want,
			) !=
				nil ||
			!reflect.DeepEqual(
				have,
				want,
			) {
			return errors.New("complete model call: terminal snapshot conflict")
		}
	}
	expected.ModelSnapshot = completion.ModelSnapshot
	expected.EndedAt = completion.EndedAt
	if !reflect.DeepEqual(expected, completion) {
		return errors.New("complete model call: terminal payload conflict")
	}
	return nil
}

// EffectiveModelPrice resolves the applicable tenant and model price at the call time.
func (r *ModelObservabilityRepository) EffectiveModelPrice(
	ctx context.Context,
	tenantID uint64,
	modelID string,
	at time.Time,
) (*types.ModelPriceVersion, error) {
	if tenantID == 0 || modelID == "" || at.IsZero() {
		return nil, errors.New("resolve model price: tenant, model, and time are required")
	}
	var price types.ModelPriceVersion
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND model_id = ? AND valid_from <= ?", tenantID, modelID, at.UTC()).
		Where("valid_to IS NULL OR valid_to > ?", at.UTC()).
		Order("valid_from DESC").
		Order("id DESC").
		First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve model price %s: %w", modelID, err)
	}
	price.ValidFrom = price.ValidFrom.UTC()
	if price.ValidTo != nil {
		value := price.ValidTo.UTC()
		price.ValidTo = &value
	}
	price.CreatedAt = price.CreatedAt.UTC()
	return &price, nil
}

// CreateModelPrice stores a non-overlapping effective price interval.
func (r *ModelObservabilityRepository) CreateModelPrice(
	ctx context.Context,
	price *types.ModelPriceVersion,
) error {
	if price == nil || price.TenantID == 0 || price.ModelID == "" || price.ValidFrom.IsZero() {
		return errors.New("create model price: tenant, model, and valid_from are required")
	}
	price.Currency = strings.ToUpper(strings.TrimSpace(price.Currency))
	if !isCurrencyCode(price.Currency) || price.InputMicrounitsPerMillion < 0 || price.OutputMicrounitsPerMillion < 0 {
		return errors.New("create model price: currency must have three letters and prices must be non-negative")
	}
	if price.CachePricing != nil {
		if price.CachePricing.Version != 1 {
			return errors.New("create model price: unsupported cache price version")
		}
		for _, value := range []*int64{
			price.CachePricing.ReadMicrounitsPerMillion,
			price.CachePricing.Write5mMicrounitsPerMillion,
			price.CachePricing.Write1hMicrounitsPerMillion,
		} {
			if value != nil && *value < 0 {
				return errors.New("create model price: cache prices must be non-negative")
			}
		}
	}
	price.ValidFrom = price.ValidFrom.UTC()
	if price.ValidTo != nil {
		value := price.ValidTo.UTC()
		if !value.After(price.ValidFrom) {
			return errors.New("create model price: valid_to must be after valid_from")
		}
		price.ValidTo = &value
	}
	if price.ID == "" {
		price.ID = uuid.NewString()
	}
	if price.CreatedAt.IsZero() {
		price.CreatedAt = time.Now().UTC()
	} else {
		price.CreatedAt = price.CreatedAt.UTC()
	}
	scope := strconv.FormatUint(price.TenantID, 10) + "\x00" + price.ModelID
	if r.db.Name() != "postgres" {
		release := acquireModelPriceScopeLock(scope)
		defer release()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockModelPriceScope(tx, scope); err != nil {
			return err
		}
		query := tx.Model(&types.ModelPriceVersion{}).
			Where("tenant_id = ? AND model_id = ?", price.TenantID, price.ModelID).
			Where("valid_to IS NULL OR valid_to > ?", price.ValidFrom)
		if price.ValidTo != nil {
			query = query.Where("valid_from < ?", *price.ValidTo)
		}
		var overlap int64
		if err := query.Count(&overlap).Error; err != nil {
			return fmt.Errorf("check model price overlap: %w", err)
		}
		if overlap > 0 {
			return errors.New("create model price: validity window overlaps an existing version")
		}
		if err := tx.Create(price).Error; err != nil {
			return fmt.Errorf("create model price: %w", err)
		}
		return nil
	})
}

func acquireModelPriceScopeLock(scope string) func() {
	modelPriceScopeLocks.Lock()
	entry := modelPriceScopeLocks.values[scope]
	if entry == nil {
		entry = &modelPriceScopeLock{}
		modelPriceScopeLocks.values[scope] = entry
	}
	entry.users++
	modelPriceScopeLocks.Unlock()

	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()
		modelPriceScopeLocks.Lock()
		entry.users--
		if entry.users == 0 {
			delete(modelPriceScopeLocks.values, scope)
		}
		modelPriceScopeLocks.Unlock()
	}
}

func lockModelPriceScope(tx *gorm.DB, scope string) error {
	if tx.Name() != "postgres" {
		return nil
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(scope))
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(hasher.Sum64())).Error; err != nil {
		return fmt.Errorf("lock model price validity scope: %w", err)
	}
	return nil
}

func isCurrencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

// ListModelPrices returns the price history for a tenant and model.
func (r *ModelObservabilityRepository) ListModelPrices(
	ctx context.Context,
	tenantID uint64,
	modelID string,
) ([]*types.ModelPriceVersion, error) {
	if tenantID == 0 || modelID == "" {
		return nil, errors.New("list model prices: tenant and model are required")
	}
	var prices []*types.ModelPriceVersion
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND model_id = ?", tenantID, modelID).
		Order("valid_from DESC").Order("id DESC").Find(&prices).Error; err != nil {
		return nil, fmt.Errorf("list model prices %s: %w", modelID, err)
	}
	for _, price := range prices {
		price.ValidFrom = price.ValidFrom.UTC()
		if price.ValidTo != nil {
			value := price.ValidTo.UTC()
			price.ValidTo = &value
		}
		price.CreatedAt = price.CreatedAt.UTC()
	}
	return prices, nil
}

var (
	_ modelobs.Store               = (*ModelObservabilityRepository)(nil)
	_ modelobs.EvaluationCostStore = (*ModelObservabilityRepository)(nil)
)
