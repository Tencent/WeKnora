// Package modelconfig holds a neutral struct that carries everything an LLM
// sub-package (chat / embedding / rerank / vlm / asr) needs in order to build
// a client from a types.Model record.
//
// Previously every sub-package had its own ConfigFromModel with almost
// identical body (ModelID/ModelName/BaseURL/APIKey/Source/Provider/ExtraConfig/
// CustomHeaders/AppID/AppSecret). That pattern is preserved here: sub-packages
// keep their public ConfigFromModel and Config shape (handlers and services
// import them directly), but they build their Config on top of this shared
// struct instead of re-listing every field.
package modelconfig

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// Base bundles the subset of types.Model that every LLM sub-package needs.
// Fields match the names used by the public Config structs so mapping is
// straightforward.
type Base struct {
	ModelID       string
	ModelName     string
	BaseURL       string
	APIKey        string
	Source        types.ModelSource
	Provider      string
	ExtraConfig   map[string]string
	CustomHeaders map[string]string
	// AppID / AppSecret are WeKnoraCloud credentials. Callers have already
	// decrypted AppSecret before building the Base — the LLM layer never
	// sees encrypted values.
	AppID     string
	AppSecret string
}

// FromModel extracts the shared fields from a types.Model. Sub-packages call
// this and then add their own extras (e.g. embedding.Config adds Dimensions
// and TruncatePromptTokens; vlm.Config adds InterfaceType / Extra).
//
// Returns zero value when m is nil — callers already handle nil models, and
// a zero Base trivially maps to a zero Config.
func FromModel(m *types.Model, appID, appSecret string) Base {
	if m == nil {
		return Base{}
	}
	return Base{
		ModelID:       m.ID,
		ModelName:     m.Name,
		BaseURL:       m.Parameters.BaseURL,
		APIKey:        m.Parameters.APIKey,
		Source:        m.Source,
		Provider:      m.Parameters.Provider,
		ExtraConfig:   m.Parameters.ExtraConfig,
		CustomHeaders: m.Parameters.CustomHeaders,
		AppID:         appID,
		AppSecret:     appSecret,
	}
}
