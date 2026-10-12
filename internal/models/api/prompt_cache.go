package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
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
func PromptPrefixFingerprint(messages []Message, opts *Options) string {
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

const openAIPromptCacheKeyMaxLength = 64

// ClampPromptCacheKey trims a routing key to the 64 characters OpenAI accepts.
func ClampPromptCacheKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= openAIPromptCacheKeyMaxLength {
		return key
	}
	return string(runes[:openAIPromptCacheKeyMaxLength])
}

// ResolveCacheRetention returns the caller's retention preference, defaulting
// to the provider's short cache.
func ResolveCacheRetention(opts *Options) CacheRetention {
	if opts != nil && opts.CacheRetention != "" {
		return opts.CacheRetention
	}
	return CacheRetentionShort
}

// CacheControlMarker is the Anthropic-style cache breakpoint object that
// several OpenAI-compatible gateways (OpenRouter, DashScope) also accept.
type CacheControlMarker struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// CacheControlFor builds the marker for a retention preference, or nil when
// the caller disabled caching for this request.
func CacheControlFor(retention CacheRetention, longTTL string) *CacheControlMarker {
	if retention == CacheRetentionNone {
		return nil
	}
	marker := &CacheControlMarker{Type: "ephemeral"}
	if retention == CacheRetentionLong && longTTL != "" {
		marker.TTL = longTTL
	}
	return marker
}

// ApplyCacheControlBreakpoints injects Anthropic-style cache_control markers
// into an OpenAI-compatible JSON body: the first instruction message, the last
// tool definition and the last conversation message.
func ApplyCacheControlBreakpoints(payload map[string]any, marker *CacheControlMarker) {
	if marker == nil {
		return
	}
	applyCacheControlToInstructionMessages(payload["messages"], marker)
	applyCacheControlToLastTool(payload["tools"], marker)
	applyCacheControlToLastConversationMessage(payload["messages"], marker)
}

func applyCacheControlToInstructionMessages(raw any, marker *CacheControlMarker) {
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

func applyCacheControlToLastConversationMessage(raw any, marker *CacheControlMarker) {
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

func applyCacheControlToLastTool(raw any, marker *CacheControlMarker) {
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

func addCacheControlToMessageContent(msg map[string]any, marker *CacheControlMarker) bool {
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

// AttachSessionAffinityHeaders sets the sticky-routing headers OpenAI-style
// gateways use to keep one conversation on one cache shard.
func AttachSessionAffinityHeaders(req *http.Request, sessionID string) {
	if req == nil || sessionID == "" {
		return
	}
	req.Header.Set("session_id", sessionID)
	req.Header.Set("x-client-request-id", sessionID)
	req.Header.Set("x-session-affinity", sessionID)
}

// WithSessionCacheKey returns opts with PromptCacheKey filled from the
// session on ctx when the caller left it empty, so calls made inside a
// session keep their cache routing key. opts itself is never mutated.
func WithSessionCacheKey(ctx context.Context, opts *Options) *Options {
	if opts != nil && opts.PromptCacheKey != "" {
		return opts
	}
	sessionID, ok := types.SessionIDFromContext(ctx)
	if !ok || sessionID == "" {
		return opts
	}
	out := Options{}
	if opts != nil {
		out = *opts
	}
	out.PromptCacheKey = sessionID
	return &out
}
