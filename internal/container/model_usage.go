package container

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func optionalPrice(name string) (float64, bool) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	return v, err == nil
}

func estimateChatCost(u *types.TokenUsage) *float64 {
	input, inputOK := optionalPrice("TOPIC3_CHAT_INPUT_PRICE_PER_MILLION_CNY")
	output, outputOK := optionalPrice("TOPIC3_CHAT_OUTPUT_PRICE_PER_MILLION_CNY")
	if !inputOK || !outputOK {
		return nil
	}
	cached, cachedOK := optionalPrice("TOPIC3_CHAT_CACHED_INPUT_PRICE_PER_MILLION_CNY")
	if !cachedOK {
		cached = input
	}
	uncachedTokens := u.PromptTokens - u.CacheReadTokens
	if uncachedTokens < 0 {
		uncachedTokens = 0
	}
	cost := (float64(uncachedTokens)*input + float64(u.CacheReadTokens)*cached + float64(u.CompletionTokens)*output) / 1_000_000
	return &cost
}

func initModelUsageSink(repo interfaces.ModelUsageRepository) {
	chat.SetUsageSink(func(ctx context.Context, model string, usage *types.TokenUsage) {
		value := strings.ToLower(strings.TrimSpace(os.Getenv("TOPIC3_MODEL_USAGE_ENABLED")))
		if value == "0" || value == "false" || value == "off" {
			return
		}
		tenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || tenantID == 0 {
			return
		}
		purpose, fingerprint := types.LLMCallMetadataFromContext(ctx)
		row := &types.ModelUsage{
			TenantID: tenantID, Model: model, CallType: "chat", ActualCalls: 1, Purpose: purpose, PromptFingerprint: fingerprint,
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens,
			CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
			CacheMissTokens: usage.CacheMissTokens, CacheReported: usage.CacheReported,
			CacheStatus: usage.CacheStatus, CostCNY: estimateChatCost(usage),
			Status: "success",
		}
		if err := repo.Create(context.WithoutCancel(ctx), row); err != nil {
			logger.Errorf(ctx, "Failed to persist model usage: %v", err)
		}
	})
}
