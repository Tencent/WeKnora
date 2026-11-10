package api

import "github.com/Tencent/WeKnora/internal/types"

// OpenAIUsage is the superset of the usage shapes OpenAI-compatible
// endpoints return. Vendors disagree on where cache counters live:
//   - OpenAI / Azure / most gateways: prompt_tokens_details.cached_tokens
//   - DeepSeek: prompt_cache_hit_tokens / prompt_cache_miss_tokens
//   - Kimi: top-level cached_tokens
//   - Anthropic-through-OpenRouter: cache_read_input_tokens / cache_creation_input_tokens
//
// All of them are decoded here so protocol code never guesses per vendor.
type OpenAIUsage struct {
	PromptTokens             int  `json:"prompt_tokens"`
	CompletionTokens         int  `json:"completion_tokens"`
	TotalTokens              int  `json:"total_tokens"`
	PromptCacheHitTokens     *int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens    *int `json:"prompt_cache_miss_tokens"`
	CachedTokens             *int `json:"cached_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	PromptTokensDetails      *struct {
		CachedTokens     *int `json:"cached_tokens"`
		CacheWriteTokens *int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// ToTokenUsage normalizes the counters. cacheAccounting states whether the
// vendor documents prompt-cache accounting at all: when it does not, a
// missing counter means "unsupported" rather than "unreported".
func (u OpenAIUsage) ToTokenUsage(cacheAccounting bool) types.TokenUsage {
	out := types.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	switch {
	case u.PromptCacheHitTokens != nil || u.PromptCacheMissTokens != nil:
		read := valueOrZero(u.PromptCacheHitTokens)
		miss := valueOrZero(u.PromptCacheMissTokens)
		out.SetPromptCacheUsage(read, 0, miss, true)
	case u.CacheReadInputTokens != nil || u.CacheCreationInputTokens != nil:
		read := valueOrZero(u.CacheReadInputTokens)
		write := valueOrZero(u.CacheCreationInputTokens)
		out.SetPromptCacheUsage(read, write, max(0, u.PromptTokens-read), true)
	case u.PromptTokensDetails != nil &&
		(u.PromptTokensDetails.CachedTokens != nil || u.PromptTokensDetails.CacheWriteTokens != nil):
		read := valueOrZero(u.PromptTokensDetails.CachedTokens)
		write := valueOrZero(u.PromptTokensDetails.CacheWriteTokens)
		out.SetPromptCacheUsage(read, write, max(0, u.PromptTokens-read), true)
	case u.CachedTokens != nil:
		read := valueOrZero(u.CachedTokens)
		out.SetPromptCacheUsage(read, 0, max(0, u.PromptTokens-read), true)
	case cacheAccounting:
		out.SetPromptCacheUsage(0, 0, 0, false)
	default:
		out.MarkPromptCacheUnsupported()
	}
	return out
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
