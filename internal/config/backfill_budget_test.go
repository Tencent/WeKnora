package config

import "testing"

// TestBackfillSummaryBudget pins the legacy-budget migration: callers read
// only Summary.MaxCompletionTokens (model-mgmt v2 budget field unification),
// so a deployment whose config.yaml still sets summary.max_tokens must keep
// its budget. An explicit max_completion_tokens always wins. The migration
// must not depend on prompt templates existing (see the call-site comment in
// config.go — outside the PromptTemplates gate).
func TestBackfillSummaryBudget(t *testing.T) {
	cases := []struct {
		name                string
		maxTokens           int
		maxCompletionTokens int
		want                int
	}{
		{"legacy max_tokens migrates", 1024, 0, 1024},
		{"explicit max_completion_tokens wins", 1024, 2048, 2048},
		{"only max_completion_tokens untouched", 0, 2048, 2048},
		{"both unset stays zero", 0, 0, 0},
		{"nil conversation config is a no-op", 1024, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			if tc.name != "nil conversation config is a no-op" {
				cfg.Conversation = &ConversationConfig{
					Summary: &SummaryConfig{
						MaxTokens:           tc.maxTokens,
						MaxCompletionTokens: tc.maxCompletionTokens,
					},
				}
			}
			backfillSummaryBudget(cfg)
			if cfg.Conversation == nil {
				return
			}
			if got := cfg.Conversation.Summary.MaxCompletionTokens; got != tc.want {
				t.Fatalf("MaxCompletionTokens = %d, want %d", got, tc.want)
			}
		})
	}
}
