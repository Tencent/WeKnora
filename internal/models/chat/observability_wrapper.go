package chat

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// observableChat emits provider-independent telemetry when the request context
// contains an LLMCallObserver. It stays enabled for every model but is a cheap
// passthrough for normal application traffic.
type observableChat struct {
	inner   Chat
	pricing types.LLMTokenPricing
}

func (o *observableChat) GetModelName() string { return o.inner.GetModelName() }
func (o *observableChat) GetModelID() string   { return o.inner.GetModelID() }

func (o *observableChat) Chat(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (*types.ChatResponse, error) {
	startedAt := time.Now()
	requestFingerprint := RequestFingerprint(ctx, messages, opts)
	response, err := o.inner.Chat(ctx, messages, opts)
	var usage types.TokenUsage
	if response != nil {
		usage = response.Usage
	}
	o.observe(ctx, usage, time.Since(startedAt), requestFingerprint, err)
	return response, err
}

func (o *observableChat) ChatStream(
	ctx context.Context,
	messages []Message,
	opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	startedAt := time.Now()
	requestFingerprint := RequestFingerprint(ctx, messages, opts)
	stream, err := o.inner.ChatStream(ctx, messages, opts)
	if err != nil || stream == nil {
		o.observe(ctx, types.TokenUsage{}, time.Since(startedAt), requestFingerprint, err)
		return stream, err
	}

	observed := make(chan types.StreamResponse)
	go func() {
		defer close(observed)
		var usage types.TokenUsage
		var streamErr error
		for response := range stream {
			if response.Usage != nil {
				usage = *response.Usage
			}
			if response.ResponseType == types.ResponseTypeError {
				streamErr = &modelStreamError{message: response.Content}
			}
			observed <- response
		}
		o.observe(ctx, usage, time.Since(startedAt), requestFingerprint, streamErr)
	}()
	return observed, nil
}

func (o *observableChat) observe(
	ctx context.Context,
	usage types.TokenUsage,
	duration time.Duration,
	requestFingerprint string,
	err error,
) {
	purpose, prefixFingerprint := types.LLMCallMetadataFromContext(ctx)
	observation := types.LLMCallObservation{
		ModelType: types.ModelTypeKnowledgeQA,
		ModelID:   o.inner.GetModelID(), ModelName: o.inner.GetModelName(),
		Purpose: purpose, PromptPrefixFingerprint: prefixFingerprint,
		RequestFingerprint: requestFingerprint,
		Usage:              usage, Pricing: o.pricing,
		EstimatedCost: types.EstimateLLMCallCost(usage, o.pricing),
		DurationMS:    duration.Milliseconds(), Success: err == nil,
	}
	if err != nil {
		observation.Error = err.Error()
	}
	types.DispatchLLMCallObservation(ctx, observation)
}

type modelStreamError struct {
	message string
}

func (e *modelStreamError) Error() string { return e.message }

func wrapChatObservability(c Chat, extraConfig map[string]string, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	return &observableChat{inner: c, pricing: pricingFromExtraConfig(extraConfig)}, nil
}

func pricingFromExtraConfig(config map[string]string) types.LLMTokenPricing {
	pricing := types.LLMTokenPricing{
		Enabled:              parsePricingBool(config["pricing_enabled"]),
		Currency:             config["pricing_currency"],
		InputPerMillion:      parseNonNegativePrice(config["input_price_per_million"]),
		OutputPerMillion:     parseNonNegativePrice(config["output_price_per_million"]),
		CacheReadPerMillion:  parseNonNegativePrice(config["cache_read_price_per_million"]),
		CacheWritePerMillion: parseNonNegativePrice(config["cache_write_price_per_million"]),
	}
	return pricing.Normalize()
}

func parsePricingBool(value string) bool {
	enabled, _ := strconv.ParseBool(strings.TrimSpace(value))
	return enabled
}

func parseNonNegativePrice(value string) float64 {
	price, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || price < 0 {
		return 0
	}
	return price
}
