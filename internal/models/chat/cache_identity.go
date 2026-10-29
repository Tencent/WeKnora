package chat

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/anthropicmessages"
	"github.com/Tencent/WeKnora/internal/models/api/googlegenai"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/models/api/openairesponses"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/processingcache"
)

type cacheIdentity struct{ identity string }

func (c cacheIdentity) ProcessingCacheIdentity() string { return c.identity }

type cacheCompletions struct {
	*openaicompletions.Client
	cacheIdentity
}
type cacheResponses struct {
	*openairesponses.Client
	cacheIdentity
}
type cacheAnthropic struct {
	*anthropicmessages.Client
	cacheIdentity
}
type cacheGoogle struct {
	*googlegenai.Client
	cacheIdentity
}

func remoteCacheIdentity(config *ChatConfig, r *modelruntime.Resolved, e api.Endpoint) cacheIdentity {
	return cacheIdentity{processingcache.Digest(map[string]any{
		"schema": 1, "config": config, "api": r.API, "spec": r.Spec, "thinking": r.ThinkingLevels,
		"completions": r.OpenAICompletions, "responses": r.OpenAIResponses, "anthropic": r.AnthropicMessages, "google": r.GoogleGenerativeAI,
		"base_url": e.BaseURL, "url": e.URL, "model": e.Model, "query": e.Query, "headers": e.Headers,
		"default_api_key": r.Vendor.DefaultAPIKey, "auth_style": r.Vendor.AuthStyleFor(r.API),
	})}
}
func cacheIdentityOf(c Chat) string {
	if i, ok := c.(interface{ ProcessingCacheIdentity() string }); ok {
		return i.ProcessingCacheIdentity()
	}
	return ""
}
func (c *concurrencyChat) ProcessingCacheIdentity() string { return cacheIdentityOf(c.inner) }
func (c *debugChat) ProcessingCacheIdentity() string       { return cacheIdentityOf(c.inner) }
func (c *langfuseChat) ProcessingCacheIdentity() string    { return cacheIdentityOf(c.inner) }
func (c *OllamaChat) ProcessingCacheIdentity() string      { return c.cacheIdentity }
