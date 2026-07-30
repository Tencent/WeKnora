package chat

import "github.com/Tencent/WeKnora/internal/models/inferencecache"

// chatDelegate avoids naming the embedded field "Chat", which would shadow
// the Chat(...) method and prevent method promotion from satisfying Chat.
type chatDelegate = Chat

type fingerprintedChat struct {
	chatDelegate
	fingerprint string
}

func (c *fingerprintedChat) CacheFingerprint() string { return c.fingerprint }

// ModelFingerprint includes every non-secret setting that can change a chat
// completion. Credential rotation and concurrency tuning do not invalidate
// deterministic enrichment results.
func ModelFingerprint(config *ChatConfig) string {
	if config == nil {
		return inferencecache.Fingerprint(nil)
	}
	return inferencecache.Fingerprint(struct {
		Source        string            `json:"source"`
		BaseURL       string            `json:"base_url"`
		ModelName     string            `json:"model_name"`
		ModelID       string            `json:"model_id"`
		Provider      string            `json:"provider"`
		AppID         string            `json:"app_id,omitempty"`
		ExtraConfig   map[string]string `json:"extra_config,omitempty"`
		CustomHeaders map[string]string `json:"custom_headers,omitempty"`
	}{
		Source:        string(config.Source),
		BaseURL:       config.BaseURL,
		ModelName:     config.ModelName,
		ModelID:       config.ModelID,
		Provider:      config.Provider,
		AppID:         config.AppID,
		ExtraConfig:   config.ExtraConfig,
		CustomHeaders: config.CustomHeaders,
	})
}

func FingerprintOf(model Chat) string {
	if identified, ok := model.(interface{ CacheFingerprint() string }); ok {
		return identified.CacheFingerprint()
	}
	if model == nil {
		return inferencecache.Fingerprint(nil)
	}
	return inferencecache.Fingerprint(struct {
		ModelID   string `json:"model_id"`
		ModelName string `json:"model_name"`
	}{ModelID: model.GetModelID(), ModelName: model.GetModelName()})
}
