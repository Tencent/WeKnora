package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
)

// FingerprintPromptPrefix returns a short, non-reversible identifier suitable
// for logs and cache routing. Raw prompts must never be used as metric labels.
func FingerprintPromptPrefix(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// PromptPrefixFingerprint hashes the stable portion common to normal chat and
// agent requests: leading system messages plus the deterministic tool schema.
// Dynamic conversation/user messages intentionally do not participate.
func PromptPrefixFingerprint(messages []Message, opts *ChatOptions) string {
	type stablePrefix struct {
		System []Message `json:"system,omitempty"`
		Tools  []Tool    `json:"tools,omitempty"`
	}
	prefix := stablePrefix{}
	for _, message := range messages {
		if message.Role != "system" {
			break
		}
		prefix.System = append(prefix.System, message)
	}
	if opts != nil {
		prefix.Tools = opts.Tools
	}
	data, _ := json.Marshal(prefix)
	return FingerprintPromptPrefix(string(data))
}

// BuildPromptCacheKey derives an opaque process-local coordination key.
// Tenant and model identifiers are hashed rather than retained in memory.
func BuildPromptCacheKey(tenantID uint64, modelID, purpose, prefixFingerprint string) string {
	return "wk-" + FingerprintPromptPrefix(
		fmt.Sprintf("%d", tenantID), modelID, purpose, prefixFingerprint,
	)
}

func providerCacheAccountingStatus(name provider.ProviderName) types.PromptCacheStatus {
	switch name {
	case provider.ProviderOpenAI,
		provider.ProviderAzureOpenAI,
		provider.ProviderDeepSeek,
		provider.ProviderAliyun,
		provider.ProviderAnthropic:
		return types.PromptCacheStatusUnreported
	default:
		return types.PromptCacheStatusUnsupported
	}
}

func tokenUsageFromOpenAI(usage openai.Usage, providerName provider.ProviderName) types.TokenUsage {
	u := types.TokenUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.PromptTokensDetails != nil {
		read := usage.PromptTokensDetails.CachedTokens
		u.SetPromptCacheUsage(read, 0, max(0, usage.PromptTokens-read), true)
		return u
	}
	if providerCacheAccountingStatus(providerName) == types.PromptCacheStatusUnsupported {
		u.MarkPromptCacheUnsupported()
	} else {
		u.SetPromptCacheUsage(0, 0, 0, false)
	}
	return u
}

// cachedTokens is retained as the nil-safe primitive used by older callers
// and focused tests; normalization happens in tokenUsageFromOpenAI.
func cachedTokens(details *openai.PromptTokensDetails) int {
	if details == nil {
		return 0
	}
	return details.CachedTokens
}

type rawPromptCacheUsage struct {
	Usage struct {
		PromptTokens        int  `json:"prompt_tokens"`
		PromptCacheHit      *int `json:"prompt_cache_hit_tokens"`
		PromptCacheMiss     *int `json:"prompt_cache_miss_tokens"`
		CacheReadInput      *int `json:"cache_read_input_tokens"`
		CacheCreationInput  *int `json:"cache_creation_input_tokens"`
		PromptTokensDetails *struct {
			CachedTokens     *int `json:"cached_tokens"`
			CacheWriteTokens *int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// applyRawPromptCacheUsage captures native fields discarded by the pinned
// OpenAI-compatible SDK (notably DeepSeek hit/miss counters).
func applyRawPromptCacheUsage(data []byte, usage *types.TokenUsage) {
	if usage == nil || len(data) == 0 {
		return
	}
	var raw rawPromptCacheUsage
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	if raw.Usage.PromptCacheHit != nil || raw.Usage.PromptCacheMiss != nil {
		read := valueOrZero(raw.Usage.PromptCacheHit)
		miss := valueOrZero(raw.Usage.PromptCacheMiss)
		usage.SetPromptCacheUsage(read, 0, miss, true)
		return
	}
	if raw.Usage.CacheReadInput != nil || raw.Usage.CacheCreationInput != nil {
		read := valueOrZero(raw.Usage.CacheReadInput)
		write := valueOrZero(raw.Usage.CacheCreationInput)
		usage.SetPromptCacheUsage(read, write, max(0, usage.PromptTokens-read), true)
		return
	}
	if details := raw.Usage.PromptTokensDetails; details != nil {
		read := valueOrZero(details.CachedTokens)
		write := valueOrZero(details.CacheWriteTokens)
		usage.SetPromptCacheUsage(read, write, max(0, usage.PromptTokens-read), true)
	}
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// CacheRetention is the prompt-cache TTL preference. Empty means short (the
// default 5-minute provider cache). Compaction/summarization uses none so a
// different prompt prefix does not occupy the session's cache slot.
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

const openAIPromptCacheKeyMaxLength = 64

func clampPromptCacheKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= openAIPromptCacheKeyMaxLength {
		return key
	}
	return string(runes[:openAIPromptCacheKeyMaxLength])
}

func resolveCacheRetention(opts *ChatOptions) CacheRetention {
	if opts != nil && opts.CacheRetention != "" {
		return opts.CacheRetention
	}
	return CacheRetentionShort
}

func promptCacheSessionID(ctx context.Context, opts *ChatOptions) string {
	if opts != nil && opts.PromptCacheKey != "" {
		return clampPromptCacheKey(opts.PromptCacheKey)
	}
	if sessionID, ok := types.SessionIDFromContext(ctx); ok {
		return clampPromptCacheKey(sessionID)
	}
	return ""
}

type promptCachePolicy struct {
	sendKey          bool
	sendCacheControl bool
	sendAffinity     bool
}

func promptCachePolicyFor(name provider.ProviderName, baseURL string) promptCachePolicy {
	switch name {
	case provider.ProviderOpenAI, provider.ProviderAzureOpenAI, provider.ProviderOpenRouter:
		return promptCachePolicy{sendKey: true, sendAffinity: true}
	case provider.ProviderAliyun:
		return promptCachePolicy{sendCacheControl: true}
	case provider.ProviderAnthropic:
		return promptCachePolicy{sendCacheControl: true}
	}
	if strings.Contains(baseURL, "api.openai.com") {
		return promptCachePolicy{sendKey: true, sendAffinity: true}
	}
	return promptCachePolicy{}
}

type cacheControlMarker struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

func cacheControlFor(retention CacheRetention, longTTL string) *cacheControlMarker {
	if retention == CacheRetentionNone {
		return nil
	}
	marker := &cacheControlMarker{Type: "ephemeral"}
	if retention == CacheRetentionLong && longTTL != "" {
		marker.TTL = longTTL
	}
	return marker
}

// applyPromptCacheToJSONBody injects provider cache routing and breakpoints
// into an already-shaped OpenAI-compatible request object. Returns the (possibly
// rewritten) body and whether the caller must send it via raw HTTP because the
// SDK struct cannot carry these fields.
func applyPromptCacheToJSONBody(
	body any,
	policy promptCachePolicy,
	sessionID string,
	retention CacheRetention,
) (any, bool, error) {
	if retention == CacheRetentionNone {
		return body, false, nil
	}
	if !policy.sendKey && !policy.sendCacheControl {
		return body, false, nil
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, false, err
	}

	rewritten := false
	if policy.sendKey && sessionID != "" {
		payload["prompt_cache_key"] = sessionID
		if retention == CacheRetentionLong {
			payload["prompt_cache_retention"] = "24h"
		}
		rewritten = true
	}
	if policy.sendCacheControl {
		marker := cacheControlFor(retention, "1h")
		if marker != nil {
			applyCacheControlBreakpoints(payload, marker)
			rewritten = true
		}
	}
	if !rewritten {
		return body, false, nil
	}
	return payload, true, nil
}

func applyCacheControlBreakpoints(payload map[string]any, marker *cacheControlMarker) {
	if marker == nil {
		return
	}
	applyCacheControlToInstructionMessages(payload["messages"], marker)
	applyCacheControlToLastTool(payload["tools"], marker)
	applyCacheControlToLastConversationMessage(payload["messages"], marker)
}

func applyCacheControlToInstructionMessages(raw any, marker *cacheControlMarker) {
	messages, ok := raw.([]any)
	if !ok {
		return
	}
	for _, item := range messages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "system" || role == "developer" {
			addCacheControlToMessageContent(msg, marker)
			return
		}
	}
}

func applyCacheControlToLastConversationMessage(raw any, marker *cacheControlMarker) {
	messages, ok := raw.([]any)
	if !ok {
		return
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "user" || role == "assistant" || role == "tool" {
			if addCacheControlToMessageContent(msg, marker) {
				return
			}
		}
	}
}

func applyCacheControlToLastTool(raw any, marker *cacheControlMarker) {
	tools, ok := raw.([]any)
	if !ok || len(tools) == 0 {
		return
	}
	last, ok := tools[len(tools)-1].(map[string]any)
	if !ok {
		return
	}
	last["cache_control"] = marker
}

func addCacheControlToMessageContent(msg map[string]any, marker *cacheControlMarker) bool {
	content, ok := msg["content"]
	if !ok || content == nil {
		return false
	}
	if text, ok := content.(string); ok {
		if text == "" {
			return false
		}
		msg["content"] = []any{
			map[string]any{
				"type":          "text",
				"text":          text,
				"cache_control": marker,
			},
		}
		return true
	}
	parts, ok := content.([]any)
	if !ok {
		return false
	}
	for i := len(parts) - 1; i >= 0; i-- {
		part, ok := parts[i].(map[string]any)
		if !ok {
			continue
		}
		if partType, _ := part["type"].(string); partType == "text" || partType == "tool_result" {
			part["cache_control"] = marker
			return true
		}
	}
	return false
}

func attachPromptCacheHeaders(req *http.Request, policy promptCachePolicy, sessionID string) {
	if req == nil || !policy.sendAffinity || sessionID == "" {
		return
	}
	req.Header.Set("session_id", sessionID)
	req.Header.Set("x-client-request-id", sessionID)
	req.Header.Set("x-session-affinity", sessionID)
}
