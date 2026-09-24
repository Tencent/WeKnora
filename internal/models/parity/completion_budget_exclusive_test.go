package parity

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkillInstallRequestKeepsOneCompletionField is #3474: installing a
// sandbox skill calls the workspace chat model (DeepSeek-V4-Flash in the
// report) with a completion budget. Summary config and older agent callers
// set both MaxTokens and MaxCompletionTokens. DeepSeek and generic
// OpenAI-compatible hosts want max_tokens; OpenAI and Volcengine Ark want
// max_completion_tokens. The other name must be absent, including when a
// model spec extra_body tries to add it. The number is MaxCompletionTokens
// when both aliases are set.
func TestSkillInstallRequestKeepsOneCompletionField(t *testing.T) {
	off := false
	both := &api.Options{
		Temperature:         0.2,
		MaxTokens:           1024,
		MaxCompletionTokens: 24576,
		Thinking:            &off,
	}
	cases := []struct {
		name     string
		provider string
		model    string
		baseURL  string
		spec     *types.ModelSpecOverride
		want     string
		drop     string
	}{
		{
			name: "deepseek official", provider: "deepseek", model: "DeepSeek-V4-Flash",
			want: "max_tokens", drop: "max_completion_tokens",
		},
		{
			name: "generic openai-compatible", provider: "generic", model: "DeepSeek-V4-Flash",
			baseURL: "http://127.0.0.1:9/v1",
			want:    "max_tokens", drop: "max_completion_tokens",
		},
		{
			name: "openai chat completions", provider: "openai", model: "gpt-4o",
			baseURL: "http://127.0.0.1:9/v1", // not api.openai.com, so Chat Completions
			want:    "max_completion_tokens", drop: "max_tokens",
		},
		{
			name: "volcengine ark", provider: "volcengine", model: "deepseek-v4-flash-ga-260731",
			want: "max_completion_tokens", drop: "max_tokens",
		},
		{
			name:     "deepseek extra_body cannot add the other field",
			provider: "deepseek", model: "DeepSeek-V4-Flash",
			spec: &types.ModelSpecOverride{Compat: map[string]any{
				"extra_body": map[string]any{"max_completion_tokens": 8192},
			}},
			want: "max_tokens", drop: "max_completion_tokens",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &chat.ChatConfig{
				Provider: tc.provider, ModelName: tc.model, BaseURL: tc.baseURL, Spec: tc.spec,
			}
			body := buildBody(t, cfg, both, true)
			assert.Equal(t, float64(24576), body[tc.want], "body=%v", body)
			_, present := body[tc.drop]
			assert.False(t, present, "%s must be absent, body=%v", tc.drop, body)
			require.NotEqual(t, tc.want, tc.drop)
		})
	}
}
