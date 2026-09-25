package models

import "github.com/Tencent/WeKnora/internal/models/api"

// Capabilities is the UI-facing summary of a resolved model.
type Capabilities struct {
	Provider        string                `json:"provider"`
	API             api.API               `json:"api"`
	Cataloged       bool                  `json:"cataloged"`
	Reasoning       bool                  `json:"reasoning"`
	ThinkingLevels  []api.ReasoningEffort `json:"thinking_levels"`
	ThinkingFormat  string                `json:"thinking_format"`
	Input           []string              `json:"input,omitempty"`
	ContextWindow   int                   `json:"context_window,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
	MaxTokensField  string                `json:"max_tokens_field,omitempty"`
}
