package modelobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/call"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const ledgerFinishTimeout = 5 * time.Second

const ledgerFinishAttempts = 3

// Store persists started and completed calls and resolves effective prices.
type Store interface {
	StartModelCall(context.Context, *types.ModelCallRecord) error
	CompleteModelCall(context.Context, types.ModelCallCompletion) error
	EffectiveModelPrice(context.Context, uint64, string, time.Time) (*types.ModelPriceVersion, error)
	CreateModelPrice(context.Context, *types.ModelPriceVersion) error
	ListModelPrices(context.Context, uint64, string) ([]*types.ModelPriceVersion, error)
}

// EvaluationCostStore aggregates the persisted model calls belonging to one evaluation task.
type EvaluationCostStore interface {
	EvaluationCost(context.Context, uint64, string) (*types.EvaluationRuntimeCost, error)
}

// Recorder creates provider-call wrappers backed by one ledger store.
type Recorder struct{ store Store }

// NewRecorder creates a model call recorder backed by the supplied ledger store.
func NewRecorder(store Store) *Recorder { return &Recorder{store: store} }

type activeCall struct {
	recorder     *Recorder
	record       *types.ModelCallRecord
	started      time.Time
	once         sync.Once
	finishErr    error
	cachePricing *types.ModelCachePricing
}

func (r *Recorder) start(ctx context.Context, model *types.Model, operation string) (*activeCall, bool, error) {
	if r == nil || r.store == nil || model == nil {
		return nil, false, nil
	}
	policy := policyFromContext(ctx)
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		tenantID = model.TenantID
	}
	started := time.Now()
	price, err := r.store.EffectiveModelPrice(ctx, tenantID, model.ID, started.UTC())
	if err != nil {
		if policy.strict {
			return nil, true, types.RecordModelAccountingError(
				ctx,
				fmt.Errorf(
					"resolve model price before provider call: %w",
					err,
				),
			)
		}
		logger.Errorf(ctx, "[modelobs] price lookup failed: %v", err)
		return nil, false, nil
	}
	callPurpose, _ := types.LLMCallMetadataFromContext(ctx)
	var cachePricing *types.ModelCachePricing
	if price != nil {
		cachePricing = price.CachePricing
	}
	snapshot, err := json.Marshal(types.ModelCallModelSnapshot{
		ID: model.ID, Name: model.Name, Type: model.Type, Source: model.Source,
		Provider: model.Parameters.Provider, ConfigSHA256: types.EvaluationModelConfigSHA256(model),
		CallPurpose: callPurpose, CachePricing: cachePricing, RequestMetadata: call.Metadata(ctx),
	})
	if err != nil {
		return nil, policy.strict, err
	}
	now := started.UTC()
	record := &types.ModelCallRecord{
		ID: uuid.NewString(), TenantID: tenantID, ModelID: model.ID,
		ModelSnapshot: types.JSON(snapshot), Purpose: policy.purpose, Operation: operation,
		StartedAt: now, Status: types.ModelCallStatusStarted,
		ProviderCacheStatus:    string(types.PromptCacheStatusUnreported),
		ApplicationCacheStatus: types.ApplicationCacheStatusUnavailable,
		CreatedAt:              now, UpdatedAt: now,
	}
	if taskID, _ := ctx.Value(types.ModelEvaluationTaskContextKey).(string); taskID != "" {
		record.EvaluationTaskID = taskID
	}
	if status, _ := ctx.Value(types.ModelApplicationCacheContextKey).(string); status != "" {
		record.ApplicationCacheStatus = status
	}
	if price != nil {
		record.PriceVersionID = &price.ID
		record.InputMicrounitsPerMillion = int64Pointer(price.InputMicrounitsPerMillion)
		record.OutputMicrounitsPerMillion = int64Pointer(price.OutputMicrounitsPerMillion)
		record.Currency = price.Currency
	}
	if err := r.store.StartModelCall(ctx, record); err != nil {
		if policy.strict {
			return nil, true, types.RecordModelAccountingError(ctx, fmt.Errorf("persist model call start: %w", err))
		}
		logger.Errorf(ctx, "[modelobs] start accounting failed: %v", err)
		return nil, false, nil
	}
	return &activeCall{recorder: r, record: record, started: started, cachePricing: cachePricing}, policy.strict, nil
}

func (c *activeCall) finish(ctx context.Context, status string, callErr error, usage *types.TokenUsage) error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		ended := time.Now()
		completion := types.ModelCallCompletion{
			ID: c.record.ID, EndedAt: ended.UTC(), DurationMs: ended.Sub(c.started).Milliseconds(),
			Status: status, ErrorCode: stableErrorCode(callErr),
			ProviderCacheStatus:    string(types.PromptCacheStatusUnreported),
			ApplicationCacheStatus: c.record.ApplicationCacheStatus,
		}
		if usage != nil {
			var snapshot map[string]any
			if err := json.Unmarshal(c.record.ModelSnapshot, &snapshot); err == nil {
				cloned := *usage
				snapshot["billing_usage"] = &cloned
				encoded, marshalErr := json.Marshal(snapshot)
				if marshalErr == nil {
					completion.ModelSnapshot = types.JSON(encoded)
				}
			}
			completion.ProviderCacheStatus = string(usage.CacheStatus)
			if completion.ProviderCacheStatus == "" {
				completion.ProviderCacheStatus = string(types.PromptCacheStatusUnreported)
			}
			if usage.CacheReported {
				completion.ProviderCacheReadTokens = intPointer(usage.CacheReadTokens)
				completion.ProviderCacheWriteTokens = intPointer(usage.CacheWriteTokens)
				completion.ProviderCacheMissTokens = intPointer(usage.CacheMissTokens)
			}
		}
		if modelUsageReported(usage) {
			completion.PromptTokens = intPointer(usage.PromptTokens)
			completion.CompletionTokens = intPointer(usage.CompletionTokens)
			completion.TotalTokens = intPointer(usage.TotalTokens)
			if status ==
				types.ModelCallStatusSuccess &&
				c.record.InputMicrounitsPerMillion !=
					nil &&
				c.record.OutputMicrounitsPerMillion !=
					nil {
				cost, err := CalculateUsageCostMicrounits(usage,
					*c.record.InputMicrounitsPerMillion, *c.record.OutputMicrounitsPerMillion, c.cachePricing)
				if err == nil {
					completion.CostMicrounits = &cost
					completion.AccountingComplete = true
				}
			}
		}
		// OpenRouter reports the routed request's USD cost, which can differ
		// from a catalog minimum or configured estimate. Retain both facts.
		if status == types.ModelCallStatusSuccess && usage != nil &&
			usage.ReportedCost != nil && c.record.Currency == "USD" {
			var snapshot map[string]any
			if json.Unmarshal(completion.ModelSnapshot, &snapshot) == nil && snapshot["provider"] == "openrouter" {
				if cost, ok := types.DecimalCostMicrounits(*usage.ReportedCost); ok {
					billing := snapshot["billing_usage"].(map[string]any)
					billing["price_estimate_microunits"] = completion.CostMicrounits
					billing["cost_source"] = "openrouter_usage_cost"
					if encoded, err := json.Marshal(snapshot); err == nil {
						completion.ModelSnapshot = types.JSON(encoded)
						completion.CostMicrounits = &cost
						completion.AccountingComplete = true
					}
				}
			}
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ledgerFinishTimeout)
		defer cancel()
		var err error
		for attempt := 0; attempt < ledgerFinishAttempts; attempt++ {
			err = c.recorder.store.CompleteModelCall(finishCtx, completion)
			if err == nil {
				break
			}
			if attempt+1 < ledgerFinishAttempts {
				delay := time.Duration(attempt+1) * 25 * time.Millisecond
				timer := time.NewTimer(delay)
				select {
				case <-finishCtx.Done():
					timer.Stop()
					err = finishCtx.Err()
					attempt = ledgerFinishAttempts
				case <-timer.C:
				}
			}
		}
		if err != nil {
			c.finishErr = fmt.Errorf("persist model call completion %s: %w", c.record.ID, err)
		}
	})
	return c.finishErr
}

func modelUsageReported(usage *types.TokenUsage) bool {
	return usage !=
		nil &&
		(usage.UsageReported ||
			usage.PromptTokens >
				0 ||
			usage.CompletionTokens >
				0 ||
			usage.TotalTokens >
				0)
}

func stableErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline_exceeded"
	default:
		return "provider_call_failed"
	}
}

func statusForError(err error) string {
	if err == nil {
		return types.ModelCallStatusSuccess
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return types.ModelCallStatusCanceled
	}
	return types.ModelCallStatusError
}

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }

// CalculateCostMicrounits applies integer half-up rounding to per-million prices.
func CalculateCostMicrounits(promptTokens, completionTokens int, inputPrice, outputPrice int64) (int64, error) {
	if promptTokens < 0 || completionTokens < 0 || inputPrice < 0 || outputPrice < 0 {
		return 0, errors.New("token counts and prices must be non-negative")
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(promptTokens)), big.NewInt(inputPrice))
	numerator.Add(numerator, new(big.Int).Mul(big.NewInt(int64(completionTokens)), big.NewInt(outputPrice)))
	numerator.Add(numerator, big.NewInt(500_000))
	numerator.Quo(numerator, big.NewInt(1_000_000))
	if !numerator.IsInt64() {
		return 0, errors.New("model call cost overflows int64")
	}
	return numerator.Int64(), nil
}

// CalculateUsageCostMicrounits prices disjoint ordinary-input, read, write and output buckets.
// Cache misses include writes for some providers, so they are never billed as an extra bucket.
func CalculateUsageCostMicrounits(
	u *types.TokenUsage,
	inputPrice, outputPrice int64,
	cache *types.ModelCachePricing,
) (
	int64,
	error,
) {
	if u == nil {
		return 0, errors.New("usage is unavailable")
	}
	if u.CacheStatus == types.PromptCacheStatusUnreported {
		return 0, errors.New("provider cache billing usage is unreported")
	}
	read, write := u.CacheReadTokens, u.CacheWriteTokens
	if read == 0 && write == 0 {
		return CalculateCostMicrounits(u.PromptTokens, u.CompletionTokens, inputPrice, outputPrice)
	}
	if !u.CacheReported || cache == nil || cache.Version != 1 || read < 0 || write < 0 || read+write > u.PromptTokens {
		return 0, errors.New("cache usage or cache price is incomplete")
	}
	numerator := new(big.Int)
	add := func(tokens int, price *int64) error {
		if tokens == 0 {
			return nil
		}
		if tokens < 0 || price == nil || *price < 0 {
			return errors.New("cache billing bucket is unpriced")
		}
		numerator.Add(numerator, new(big.Int).Mul(big.NewInt(int64(tokens)), big.NewInt(*price)))
		return nil
	}
	ordinary := u.PromptTokens - read - write
	if inputPrice < 0 || outputPrice < 0 || u.CompletionTokens < 0 {
		return 0, errors.New("negative tokens or prices")
	}
	if err := add(ordinary, &inputPrice); err != nil {
		return 0, err
	}
	if err := add(u.CompletionTokens, &outputPrice); err != nil {
		return 0, err
	}
	if err := add(read, cache.ReadMicrounitsPerMillion); err != nil {
		return 0, err
	}
	if write > 0 {
		if u.CacheWrite5mTokens ==
			nil ||
			u.CacheWrite1hTokens ==
				nil ||
			*u.CacheWrite5mTokens+
				*u.CacheWrite1hTokens !=
				write {
			return 0, errors.New("cache write retention buckets are unreported")
		}
		if err := add(*u.CacheWrite5mTokens, cache.Write5mMicrounitsPerMillion); err != nil {
			return 0, err
		}
		if err := add(*u.CacheWrite1hTokens, cache.Write1hMicrounitsPerMillion); err != nil {
			return 0, err
		}
	}
	numerator.Add(numerator, big.NewInt(500_000))
	numerator.Quo(numerator, big.NewInt(1_000_000))
	if !numerator.IsInt64() {
		return 0, errors.New("model call cost overflows int64")
	}
	return numerator.Int64(), nil
}
