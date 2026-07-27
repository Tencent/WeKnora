package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// logUsage emits the standard "[LLM Usage]" line shared by every Chat
// implementation. It is a no-op when usage is nil so callers can pass through
// optional usage blocks without guarding at each call site.
func logUsage(ctx context.Context, model string, u *types.TokenUsage) {
	if u == nil {
		return
	}
	purpose, prefixFingerprint := types.LLMCallMetadataFromContext(ctx)
	logger.Infof(ctx,
		"[LLM Usage] model=%s, purpose=%s, prompt_prefix=%s, prompt_tokens=%d, completion_tokens=%d, total_tokens=%d, cached_tokens=%d, cache_read_tokens=%d, cache_write_tokens=%d, cache_miss_tokens=%d, cache_reported=%t, cache_status=%s",
		model, purpose, prefixFingerprint, u.PromptTokens, u.CompletionTokens, u.TotalTokens,
		u.CachedTokens, u.CacheReadTokens, u.CacheWriteTokens, u.CacheMissTokens,
		u.CacheReported, u.CacheStatus)
}
