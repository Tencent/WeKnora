package api

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
)

// SanitizeReasoningEffort validates a stored or configured level on its way
// into Options. Callers that read the level out of a database row, a YAML
// agent or a session config cast a plain string, so this is where a typo is
// caught and dropped: an unknown level must never reach the vendor, and must
// never be mistaken for "thinking on". Returning "" means "no preference",
// leaving the legacy Thinking boolean to decide.
func SanitizeReasoningEffort(ctx context.Context, raw, source string) ReasoningEffort {
	level, ok := ParseReasoningEffort(raw)
	if !ok {
		logger.Warnf(ctx, "[Reasoning] ignoring invalid reasoning_effort %q from %s (expected one of %v)",
			raw, source, AllReasoningEfforts)
		return ""
	}
	return level
}

// CacheRetention is the prompt-cache TTL preference. Empty means short (the
// default 5-minute provider cache). Compaction/summarization uses none so a
// different prompt prefix does not occupy the session's cache slot.
type CacheRetention string

// Cache retention preferences.
const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

// Options 聊天选项。It is protocol-neutral: every field is a capability the
// caller asks for, and the protocol package decides how (or whether) the
// vendor can honour it.
type Options struct {
	Temperature float64 `json:"temperature"` // 温度参数
	TopP        float64 `json:"top_p"`       // Top P 参数
	Seed        int     `json:"seed"`        // 随机种子
	// MaxTokens and MaxCompletionTokens are aliases for one completion budget.
	// Callers may set either; CompletionBudget() prefers MaxCompletionTokens.
	// The outbound JSON carries exactly one field, named per vendor.
	MaxTokens           int     `json:"max_tokens"`
	MaxCompletionTokens int     `json:"max_completion_tokens"`
	FrequencyPenalty    float64 `json:"frequency_penalty"` // 频率惩罚
	PresencePenalty     float64 `json:"presence_penalty"`  // 存在惩罚
	// Thinking is the legacy on/off switch. Prefer ReasoningEffort; when both
	// are set ReasoningEffort wins. true maps to ReasoningAuto, false to
	// ReasoningOff.
	Thinking *bool `json:"thinking"`
	// ReasoningEffort selects the thinking intensity. Empty leaves the model
	// default untouched (no thinking fields are sent unless the vendor
	// requires them on every request).
	ReasoningEffort ReasoningEffort `json:"reasoning_effort,omitempty"`
	// ThinkingBudgetTokens caps the thinking tokens where the vendor exposes a
	// budget (Anthropic budget_tokens, DashScope thinking_budget, Gemini
	// thinkingBudget). 0 leaves the vendor default.
	ThinkingBudgetTokens int    `json:"thinking_budget_tokens,omitempty"`
	Tools                []Tool `json:"tools,omitempty"` // 可用工具列表
	// ToolChoice is "auto", "required", "none" or a specific tool name.
	ToolChoice        string          `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"` // 是否允许并行工具调用（默认 nil 表示由模型决定）
	Format            json.RawMessage `json:"format,omitempty"`              // 响应格式定义
	// PromptCacheKey is the provider routing key (OpenAI prompt_cache_key).
	// Empty falls back to the session ID on the call context.
	PromptCacheKey string `json:"-"`
	// CacheRetention controls provider prompt-cache TTL. none disables cache
	// markers; empty/short is the default 5-minute cache; long requests 1h/24h
	// where the provider accepts it.
	CacheRetention CacheRetention `json:"-"`
}

// CompletionBudget is the single per-call generation cap. MaxTokens and
// MaxCompletionTokens on Options are input aliases for the same budget
// (YAML, older callers, and the agent UI all feed one of them). When both are
// set, the newer MaxCompletionTokens wins.
func (o *Options) CompletionBudget() int {
	if o == nil {
		return 0
	}
	if o.MaxCompletionTokens > 0 {
		return o.MaxCompletionTokens
	}
	return o.MaxTokens
}

// Reasoning resolves the requested thinking level from ReasoningEffort and
// the legacy Thinking boolean. The bool result reports whether the caller
// expressed any preference at all; when false the model default applies.
//
// The stored level is re-parsed here rather than trusted: the write paths
// validate, but rows persisted before that validation existed, builtin YAML
// agents and SummaryConfig all reach this struct through a plain string cast.
// An unparseable value ("hgih") is non-empty, so Enabled() would read it as
// "thinking on" and the protocol packages would send the vendor a level it
// never defined. Ignoring it falls back to the legacy boolean, which is what
// "no usable preference" has always meant.
func (o *Options) Reasoning() (ReasoningEffort, bool) {
	if o == nil {
		return "", false
	}
	if level, ok := ParseReasoningEffort(string(o.ReasoningEffort)); ok && level != "" {
		return level, true
	}
	if o.Thinking != nil {
		if *o.Thinking {
			return ReasoningAuto, true
		}
		return ReasoningOff, true
	}
	return "", false
}

// ThinkingRequested reports whether the caller asked for thinking to be on.
func (o *Options) ThinkingRequested() bool {
	level, ok := o.Reasoning()
	return ok && level.Enabled()
}
