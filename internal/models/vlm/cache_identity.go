package vlm

import "github.com/Tencent/WeKnora/internal/processingcache"

func identityOf(v VLM) string {
	if i, ok := v.(interface{ ProcessingCacheIdentity() string }); ok {
		return i.ProcessingCacheIdentity()
	}
	return ""
}
func (v *debugVLM) ProcessingCacheIdentity() string       { return identityOf(v.inner) }
func (v *langfuseVLM) ProcessingCacheIdentity() string    { return identityOf(v.inner) }
func (v *concurrencyVLM) ProcessingCacheIdentity() string { return identityOf(v.inner) }

// ProcessingCacheIdentity includes the current effective token budget.
func (v *RemoteAPIVLM) ProcessingCacheIdentity() string {
	if v.cacheIdentity == "" {
		return ""
	}
	return processingcache.Digest([]any{v.cacheIdentity, vlmMaxTokens()})
}

// ProcessingCacheIdentity returns the immutable Ollama configuration digest.
func (v *OllamaVLM) ProcessingCacheIdentity() string { return v.cacheIdentity }

// ProcessingCacheIdentity returns the immutable cloud configuration digest.
func (v *WeKnoraCloudVLM) ProcessingCacheIdentity() string { return v.cacheIdentity }

func remoteVLMIdentity(v *RemoteAPIVLM) string {
	i, ok := v.chat.(interface{ ProcessingCacheIdentity() string })
	if !ok || i.ProcessingCacheIdentity() == "" {
		return ""
	}
	return processingcache.Digest(struct {
		Schema      int
		Chat        string
		Temperature float64
		MaxTokens   int
	}{1, i.ProcessingCacheIdentity(), v.temperature, defaultMaxToks})
}
