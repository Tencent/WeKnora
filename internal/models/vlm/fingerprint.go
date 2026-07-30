package vlm

import "github.com/Tencent/WeKnora/internal/models/inferencecache"

type fingerprintedVLM struct {
	VLM
	fingerprint string
}

func (v *fingerprintedVLM) CacheFingerprint() string { return v.fingerprint }

// ModelFingerprint scopes cached image analysis to all non-secret settings
// that can alter a VLM response.
func ModelFingerprint(config *Config) string {
	if config == nil {
		return inferencecache.Fingerprint(nil)
	}
	return inferencecache.Fingerprint(struct {
		Source        string            `json:"source"`
		BaseURL       string            `json:"base_url"`
		ModelName     string            `json:"model_name"`
		ModelID       string            `json:"model_id"`
		InterfaceType string            `json:"interface_type"`
		Provider      string            `json:"provider"`
		AppID         string            `json:"app_id,omitempty"`
		Extra         map[string]any    `json:"extra,omitempty"`
		CustomHeaders map[string]string `json:"custom_headers,omitempty"`
	}{
		Source:        string(config.Source),
		BaseURL:       config.BaseURL,
		ModelName:     config.ModelName,
		ModelID:       config.ModelID,
		InterfaceType: config.InterfaceType,
		Provider:      config.Provider,
		AppID:         config.AppID,
		Extra:         config.Extra,
		CustomHeaders: config.CustomHeaders,
	})
}

func FingerprintOf(model VLM) string {
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
