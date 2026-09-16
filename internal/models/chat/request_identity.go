package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// EffectiveRequestKey includes options that are intentionally excluded from
// provider-neutral JSON, including cache routing and retention.
func EffectiveRequestKey(
	ctx context.Context,
	tenant uint64,
	model, fingerprint string,
	messages []Message,
	opts *ChatOptions,
) string {
	payload, _ := json.Marshal(struct {
		Tenant      uint64         `json:"tenant"`
		Model       string         `json:"model"`
		Fingerprint string         `json:"fingerprint"`
		Messages    []Message      `json:"messages"`
		Options     *ChatOptions   `json:"options"`
		CacheKey    string         `json:"cache_key"`
		Retention   CacheRetention `json:"retention"`
	}{tenant, model, fingerprint, messages, opts, promptCacheSessionID(ctx, opts), resolveCacheRetention(opts)})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
