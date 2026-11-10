// Package models defines shared, credential-free model metadata.
package models

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// ModelCost is priced per million tokens, in USD unless Currency says otherwise.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Currency   string  `json:"currency,omitempty"`
}

// ModelSpec is one entry of the generated model catalog.
//
// Only ID is required. API defaults to the vendor's API; Type defaults to
// KnowledgeQA. Compat is interpreted according to API (see compat.go).
type ModelSpec struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Type is the WeKnora model type this entry describes. Chat models with
	// image input are still KnowledgeQA; the UI derives VLM eligibility from
	// Input. Embedding / Rerank / ASR entries exist for the model picker.
	Type types.ModelType `json:"type,omitempty"`
	API  api.API         `json:"api,omitempty"`
	// Aliases are additional ids that resolve to this entry (dated
	// snapshots, vendor-prefixed names on gateways).
	Aliases []string `json:"aliases,omitempty"`
	// Match is a case-insensitive glob ("gpt-5*", "*deepseek-r1*") that lets
	// one entry describe a family. Exact ids and aliases win over patterns;
	// among patterns the longest literal prefix wins.
	Match string `json:"match,omitempty"`
	// Reasoning reports whether the model can emit thinking at all.
	Reasoning bool `json:"reasoning"`
	// Input lists accepted modalities: text, image, audio, video.
	Input           []string   `json:"input,omitempty"`
	ContextWindow   int        `json:"context_window,omitempty"`
	MaxOutputTokens int        `json:"max_output_tokens,omitempty"`
	Cost            *ModelCost `json:"cost,omitempty"`
	// ThinkingLevels maps protocol-neutral levels to vendor values; missing
	// keys inherit the vendor map. See api.ThinkingLevelMap.
	ThinkingLevels api.ThinkingLevelMap `json:"thinking_levels,omitempty"`
	// Compat is the protocol-specific overlay for this model (flat object,
	// fields depend on API). Decoded lazily by Resolve.
	Compat json.RawMessage `json:"compat,omitempty"`
	// Dimension is the embedding vector width (embedding entries only).
	Dimension int `json:"dimension,omitempty"`
	// Deprecated hides the entry from pickers but keeps resolution working.
	Deprecated bool `json:"deprecated,omitempty"`
	// Source is the documentation URL the facts were taken from.
	Source string `json:"source,omitempty"`
}

// DisplayName returns Name or the id.
func (m ModelSpec) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// AcceptsImages reports whether Input lists image.
func (m ModelSpec) AcceptsImages() bool {
	for _, in := range m.Input {
		if in == "image" {
			return true
		}
	}
	return false
}
