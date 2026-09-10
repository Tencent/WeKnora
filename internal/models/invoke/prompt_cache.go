package invoke

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Ported from v1 internal/models/chat/prompt_cache.go (P1c caller sweep):
// prompt-cache coordination identifiers. None of these touch the wire — they
// are opaque routing/metric keys computed call-side.

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
		Tools  []ToolDef `json:"tools,omitempty"`
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
