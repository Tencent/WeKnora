package repository

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type modelUsageAggregationRow struct {
	CallType          types.CallType
	ModelID           string
	ModelName         string
	ModelType         string
	ModelSource       string
	ResolvedProvider  string
	ResolvedModelName *string

	InputTokens  *int
	OutputTokens *int
	TotalTokens  *int
	LatencyMS    *int64

	PromptCacheStatus    *types.PromptCacheStatus
	CacheReadTokens      *int
	CacheWriteTokens     *int
	CacheMissTokens      *int
	EmbeddingCacheStatus *types.EmbeddingCacheStatus

	LogicalRequests  int
	ProviderRequests int
	ProviderInputs   int
	ProviderPairs    int
	EmbeddingInputs  int
	CacheHits        int
	CacheMisses      int
	CacheReadErrors  int
	CacheWriteErrors int
	Queries          int
	Documents        int
	Pairs            int

	CostID       *string
	CostStatus   *types.CostStatus
	CostCurrency *string
	TotalCost    *types.Decimal
	KnownCost    *types.Decimal
}

func (r *modelUsageRepository) AggregateEvaluationRun(
	ctx context.Context, tenantID uint64, evaluationRunID string,
) (*types.EvaluationModelUsageAggregate, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("model_usage aggregation: tenant_id must be non-zero")
	}
	if evaluationRunID == "" {
		return nil, fmt.Errorf("model_usage aggregation: evaluation_run_id is required")
	}
	var rows []modelUsageAggregationRow
	err := r.db.WithContext(ctx).Table("model_usage AS u").
		Select(`u.call_type, u.model_id, u.model_name, u.model_type, u.model_source,
			u.resolved_provider, u.resolved_model_name,
			u.input_tokens, u.output_tokens, u.total_tokens, u.latency_ms,
			u.prompt_cache_status, u.cache_read_tokens, u.cache_write_tokens, u.cache_miss_tokens,
			u.embedding_cache_status, u.logical_requests, u.provider_requests, u.provider_inputs,
			u.provider_pairs, u.embedding_inputs, u.cache_hits, u.cache_misses,
			u.cache_read_errors, u.cache_write_errors, u.queries, u.documents, u.pairs,
			c.id AS cost_id, c.status AS cost_status, c.currency AS cost_currency,
			c.total_cost, c.known_cost`).
		Joins("LEFT JOIN model_usage_cost AS c ON c.usage_id = u.id").
		Where("u.tenant_id = ? AND u.evaluation_run_id = ?", tenantID, evaluationRunID).
		Order("u.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("aggregate model usage for evaluation run: %w", err)
	}

	result := &types.EvaluationModelUsageAggregate{
		ObservedModels: make([]types.ObservedModelIdentity, 0),
		CostByCurrency: make([]types.CurrencyCostAggregate, 0),
	}
	costs := make(map[string]*currencyCostAccumulator)
	observedModels := make(map[observedModelKey]int64)
	for i := range rows {
		row := &rows[i]
		if err := aggregateUsageRow(result, costs, row); err != nil {
			return nil, fmt.Errorf("aggregate model usage for evaluation run: %w", err)
		}
		observedModels[observedModelKeyFromRow(row)]++
	}
	finishNullableMetric(&result.InputTokens)
	finishNullableMetric(&result.OutputTokens)
	finishNullableMetric(&result.TotalTokens)
	finishNullableMetric(&result.CacheReadTokens)
	finishNullableMetric(&result.CacheWriteTokens)
	finishNullableMetric(&result.CacheMissTokens)
	finishLatency(&result.Latency)

	currencies := make([]string, 0, len(costs))
	for currency := range costs {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	for _, currency := range currencies {
		item, err := costs[currency].finish(currency)
		if err != nil {
			return nil, fmt.Errorf("aggregate model usage for evaluation run: %w", err)
		}
		result.CostByCurrency = append(result.CostByCurrency, item)
	}
	result.ObservedModels = finishObservedModels(observedModels)
	return result, nil
}

type observedModelKey struct {
	CallType          types.CallType
	ModelID           string
	ModelName         string
	ModelType         string
	ModelSource       string
	ResolvedProvider  string
	ResolvedModelName string
	ResolvedNameKnown bool
}

func observedModelKeyFromRow(row *modelUsageAggregationRow) observedModelKey {
	key := observedModelKey{
		CallType: row.CallType, ModelID: row.ModelID, ModelName: row.ModelName,
		ModelType: row.ModelType, ModelSource: row.ModelSource, ResolvedProvider: row.ResolvedProvider,
	}
	if row.ResolvedModelName != nil {
		key.ResolvedModelName = *row.ResolvedModelName
		key.ResolvedNameKnown = true
	}
	return key
}

func finishObservedModels(groups map[observedModelKey]int64) []types.ObservedModelIdentity {
	keys := make([]observedModelKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := keys[i], keys[j]
		leftFields := []string{
			string(left.CallType), left.ModelID, left.ModelName,
			left.ModelType, left.ModelSource, left.ResolvedProvider,
		}
		rightFields := []string{
			string(right.CallType), right.ModelID, right.ModelName,
			right.ModelType, right.ModelSource, right.ResolvedProvider,
		}
		for index := range leftFields {
			if leftFields[index] != rightFields[index] {
				return leftFields[index] < rightFields[index]
			}
		}
		if left.ResolvedNameKnown != right.ResolvedNameKnown {
			return !left.ResolvedNameKnown
		}
		return left.ResolvedModelName < right.ResolvedModelName
	})

	result := make([]types.ObservedModelIdentity, 0, len(keys))
	for _, key := range keys {
		var resolvedModelName *string
		if key.ResolvedNameKnown {
			value := key.ResolvedModelName
			resolvedModelName = &value
		}
		result = append(result, types.ObservedModelIdentity{
			CallType: key.CallType, ModelID: key.ModelID, ModelName: key.ModelName,
			ModelType: key.ModelType, ModelSource: key.ModelSource,
			ResolvedProvider: key.ResolvedProvider, ResolvedModelName: resolvedModelName,
			Calls: groups[key],
		})
	}
	return result
}

func aggregateUsageRow(
	result *types.EvaluationModelUsageAggregate,
	costs map[string]*currencyCostAccumulator,
	row *modelUsageAggregationRow,
) error {
	result.Calls.Total++
	switch row.CallType {
	case types.CallTypeChat:
		result.Calls.Chat++
	case types.CallTypeEmbedding:
		result.Calls.Embedding++
	case types.CallTypeRerank:
		result.Calls.Rerank++
	default:
		return fmt.Errorf("unknown call_type %q", row.CallType)
	}

	addNullableMetric(&result.InputTokens, row.InputTokens, true)
	addNullableMetric(&result.TotalTokens, row.TotalTokens, true)
	addNullableMetric(&result.OutputTokens, row.OutputTokens, row.CallType == types.CallTypeChat)
	addNullableMetric(&result.CacheReadTokens, row.CacheReadTokens, row.CallType == types.CallTypeChat)
	addNullableMetric(&result.CacheWriteTokens, row.CacheWriteTokens, row.CallType == types.CallTypeChat)
	addNullableMetric(&result.CacheMissTokens, row.CacheMissTokens, row.CallType == types.CallTypeChat)
	addLatency(&result.Latency, row.LatencyMS)

	result.Counters.LogicalRequests += int64(row.LogicalRequests)
	result.Counters.ProviderRequests += int64(row.ProviderRequests)
	result.Counters.ProviderInputs += int64(row.ProviderInputs)
	result.Counters.ProviderPairs += int64(row.ProviderPairs)
	result.Counters.EmbeddingInputs += int64(row.EmbeddingInputs)
	result.Counters.CacheHits += int64(row.CacheHits)
	result.Counters.CacheMisses += int64(row.CacheMisses)
	result.Counters.CacheReadErrors += int64(row.CacheReadErrors)
	result.Counters.CacheWriteErrors += int64(row.CacheWriteErrors)
	result.Counters.Queries += int64(row.Queries)
	result.Counters.Documents += int64(row.Documents)
	result.Counters.Pairs += int64(row.Pairs)

	if row.CallType == types.CallTypeChat {
		switch {
		case row.PromptCacheStatus == nil:
			result.PromptCache.NotRecorded++
		case *row.PromptCacheStatus == types.PromptCacheStatusUnreported:
			result.PromptCache.Unreported++
		case *row.PromptCacheStatus == types.PromptCacheStatusUnsupported:
			result.PromptCache.Unsupported++
		case *row.PromptCacheStatus == types.PromptCacheStatusMiss:
			result.PromptCache.Miss++
		case *row.PromptCacheStatus == types.PromptCacheStatusHit:
			result.PromptCache.Hit++
		default:
			return fmt.Errorf("unknown prompt_cache_status %q", *row.PromptCacheStatus)
		}
	}
	if row.CallType == types.CallTypeEmbedding {
		switch {
		case row.EmbeddingCacheStatus == nil:
			result.EmbeddingCache.NotRecorded++
		case *row.EmbeddingCacheStatus == types.EmbeddingCacheStatusDisabled:
			result.EmbeddingCache.Disabled++
		case *row.EmbeddingCacheStatus == types.EmbeddingCacheStatusFullHit:
			result.EmbeddingCache.FullHit++
		case *row.EmbeddingCacheStatus == types.EmbeddingCacheStatusPartial:
			result.EmbeddingCache.Partial++
		case *row.EmbeddingCacheStatus == types.EmbeddingCacheStatusMiss:
			result.EmbeddingCache.Miss++
		default:
			return fmt.Errorf("unknown embedding_cache_status %q", *row.EmbeddingCacheStatus)
		}
	}

	if row.CostID == nil {
		incrementCallCounts(&result.NoCostRowCalls, row.CallType)
		return nil
	}
	if row.CostStatus == nil {
		return fmt.Errorf("cost row %q has no status", *row.CostID)
	}
	if row.CostCurrency == nil || *row.CostCurrency == "" {
		incrementCallCounts(&result.CostRowsWithoutCurrency, row.CallType)
		return nil
	}
	accumulator := costs[*row.CostCurrency]
	if accumulator == nil {
		accumulator = newCurrencyCostAccumulator()
		costs[*row.CostCurrency] = accumulator
	}
	return accumulator.add(row.CallType, *row.CostStatus, row.TotalCost, row.KnownCost)
}

func incrementCallCounts(counts *types.CallCounts, callType types.CallType) {
	counts.Total++
	switch callType {
	case types.CallTypeChat:
		counts.Chat++
	case types.CallTypeEmbedding:
		counts.Embedding++
	case types.CallTypeRerank:
		counts.Rerank++
	}
}

func addNullableMetric(metric *types.NullableMetricAggregate, value *int, applicable bool) {
	if !applicable {
		return
	}
	metric.ApplicableCalls++
	if value == nil {
		return
	}
	metric.ObservedCalls++
	if metric.Sum == nil {
		zero := int64(0)
		metric.Sum = &zero
	}
	*metric.Sum += int64(*value)
}

func finishNullableMetric(metric *types.NullableMetricAggregate) {
	if metric.ObservedCalls == 0 {
		metric.Sum = nil
	}
}

func addLatency(latency *types.LatencyAggregate, value *int64) {
	latency.ApplicableCalls++
	if value == nil {
		return
	}
	latency.ObservedCalls++
	if latency.SumMS == nil {
		zero := int64(0)
		latency.SumMS = &zero
		maximum := *value
		latency.MaxMS = &maximum
	}
	*latency.SumMS += *value
	if *value > *latency.MaxMS {
		*latency.MaxMS = *value
	}
}

func finishLatency(latency *types.LatencyAggregate) {
	if latency.ObservedCalls == 0 {
		latency.SumMS, latency.MaxMS, latency.AverageMS = nil, nil, nil
		return
	}
	average := float64(*latency.SumMS) / float64(latency.ObservedCalls)
	latency.AverageMS = &average
}

type callTypeCostAccumulator struct {
	priced                                   *big.Rat
	known                                    *big.Rat
	pricedCalls, partialCalls, unpricedCalls int64
}

type currencyCostAccumulator struct {
	chat, embedding, rerank, total callTypeCostAccumulator
}

func newCurrencyCostAccumulator() *currencyCostAccumulator {
	result := &currencyCostAccumulator{}
	for _, accumulator := range []*callTypeCostAccumulator{
		&result.chat, &result.embedding, &result.rerank, &result.total,
	} {
		accumulator.priced, accumulator.known = new(big.Rat), new(big.Rat)
	}
	return result
}

func (c *currencyCostAccumulator) add(
	callType types.CallType, status types.CostStatus, totalCost, knownCost *types.Decimal,
) error {
	var byType *callTypeCostAccumulator
	switch callType {
	case types.CallTypeChat:
		byType = &c.chat
	case types.CallTypeEmbedding:
		byType = &c.embedding
	case types.CallTypeRerank:
		byType = &c.rerank
	default:
		return fmt.Errorf("unknown call_type %q", callType)
	}
	for _, target := range []*callTypeCostAccumulator{byType, &c.total} {
		switch status {
		case types.CostStatusPriced:
			target.pricedCalls++
			if totalCost == nil {
				return fmt.Errorf("priced cost is missing total_cost")
			}
			if err := addExactDecimal(target.priced, *totalCost); err != nil {
				return err
			}
		case types.CostStatusPartial:
			target.partialCalls++
		case types.CostStatusUnpriced:
			target.unpricedCalls++
		default:
			return fmt.Errorf("unknown cost status %q", status)
		}
		if knownCost != nil {
			if err := addExactDecimal(target.known, *knownCost); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *currencyCostAccumulator) finish(currency string) (types.CurrencyCostAggregate, error) {
	chat, err := c.chat.finish()
	if err != nil {
		return types.CurrencyCostAggregate{}, err
	}
	embedding, err := c.embedding.finish()
	if err != nil {
		return types.CurrencyCostAggregate{}, err
	}
	rerank, err := c.rerank.finish()
	if err != nil {
		return types.CurrencyCostAggregate{}, err
	}
	total, err := c.total.finish()
	if err != nil {
		return types.CurrencyCostAggregate{}, err
	}
	return types.CurrencyCostAggregate{
		Currency: currency, Chat: chat, Embedding: embedding, Rerank: rerank, Total: total,
	}, nil
}

func (c *callTypeCostAccumulator) finish() (types.CallTypeCostAggregate, error) {
	priced, err := exactDecimalFromRat(c.priced)
	if err != nil {
		return types.CallTypeCostAggregate{}, err
	}
	known, err := exactDecimalFromRat(c.known)
	if err != nil {
		return types.CallTypeCostAggregate{}, err
	}
	return types.CallTypeCostAggregate{
		PricedCost: priced, KnownCost: known, PricedCalls: c.pricedCalls,
		PartialCalls: c.partialCalls, UnpricedCalls: c.unpricedCalls,
	}, nil
}

func addExactDecimal(total *big.Rat, value types.Decimal) error {
	if err := value.Validate("cost"); err != nil {
		return err
	}
	parsed, ok := new(big.Rat).SetString(string(value))
	if !ok {
		return fmt.Errorf("invalid cost decimal %q", value)
	}
	total.Add(total, parsed)
	return nil
}

// exactDecimalFromRat emits the canonical terminating decimal form. Every
// input originates as a base-10 Decimal, so a non-terminating denominator is a
// data-contract violation rather than something to round.
func exactDecimalFromRat(value *big.Rat) (types.Decimal, error) {
	if value.Sign() == 0 {
		return types.Decimal("0"), nil
	}
	denominator := new(big.Int).Set(value.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	countTwo, countFive := 0, 0
	rem := new(big.Int)
	for {
		q := new(big.Int)
		q.QuoRem(denominator, two, rem)
		if rem.Sign() != 0 {
			break
		}
		denominator = q
		countTwo++
	}
	for {
		q := new(big.Int)
		q.QuoRem(denominator, five, rem)
		if rem.Sign() != 0 {
			break
		}
		denominator = q
		countFive++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("cost sum is not a terminating decimal")
	}
	scale := countTwo
	if countFive > scale {
		scale = countFive
	}
	raw := value.FloatString(scale)
	if strings.Contains(raw, ".") {
		raw = strings.TrimRight(strings.TrimRight(raw, "0"), ".")
	}
	return types.Decimal(raw), nil
}
