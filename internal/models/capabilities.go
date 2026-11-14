package models

import "github.com/Tencent/WeKnora/internal/models/api"

// Capabilities is the UI-facing summary of a resolved model.
type Capabilities struct {
	Provider        string                `json:"provider"`
	API             api.API               `json:"api" extensions:"x-agent"`
	Cataloged       bool                  `json:"cataloged" extensions:"x-agent"`
	Reasoning       bool                  `json:"reasoning" extensions:"x-agent"`
	ThinkingLevels  []api.ReasoningEffort `json:"thinking_levels" extensions:"x-agent"`
	ThinkingFormat  string                `json:"thinking_format"`
	Input           []string              `json:"input,omitempty" extensions:"x-agent"`
	ContextWindow   int                   `json:"context_window,omitempty" extensions:"x-agent"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty" extensions:"x-agent"`
	MaxTokensField  string                `json:"max_tokens_field,omitempty"`
}
