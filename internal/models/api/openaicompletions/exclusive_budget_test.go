package openaicompletions

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompletionBudgetFieldsAreMutuallyExclusive is the #3474 regression:
// DeepSeek, Volcengine Ark and other OpenAI-compatible gateways return
// 400 InvalidParameter when one chat request carries both max_tokens and
// max_completion_tokens. The skill installer (and SummaryConfig) can
// configure both aliases; the wire body must keep exactly one, named the
// way the provider documents it. When both aliases are set, the numeric
// budget is MaxCompletionTokens (see api.Options.CompletionBudget).
func TestCompletionBudgetFieldsAreMutuallyExclusive(t *testing.T) {
	msgs := []api.Message{{Role: "user", Content: "install the skill"}}
	off := false

	t.Run("deepseek field keeps max_tokens when both aliases are set", func(t *testing.T) {
		c := newClient(t, func(cfg *Config) {
			cfg.Settings.MaxTokensField = "max_tokens"
		})
		got := bodyJSON(t, c, msgs, &api.Options{
			Temperature:         0.2,
			MaxTokens:           1024,
			MaxCompletionTokens: 24576,
			Thinking:            &off,
		}, true)
		assert.Equal(t, float64(24576), got["max_tokens"])
		_, hasOther := got["max_completion_tokens"]
		assert.False(t, hasOther, "max_completion_tokens must be absent, body=%v", got)
	})

	t.Run("default field keeps max_completion_tokens when both aliases are set", func(t *testing.T) {
		c := newClient(t, nil)
		got := bodyJSON(t, c, msgs, &api.Options{
			MaxTokens:           1024,
			MaxCompletionTokens: 24576,
		}, false)
		assert.Equal(t, float64(24576), got["max_completion_tokens"])
		_, hasOther := got["max_tokens"]
		assert.False(t, hasOther, "max_tokens must be absent, body=%v", got)
	})

	t.Run("extra_body must not put the other name back", func(t *testing.T) {
		c := newClient(t, func(cfg *Config) {
			cfg.Settings.MaxTokensField = "max_tokens"
			cfg.Settings.ExtraBody = map[string]any{"max_completion_tokens": 8192}
		})
		got := bodyJSON(t, c, msgs, &api.Options{MaxCompletionTokens: 24576}, true)
		assert.Equal(t, float64(24576), got["max_tokens"])
		_, hasOther := got["max_completion_tokens"]
		assert.False(t, hasOther, "extra_body reintroduced max_completion_tokens, body=%v", got)
	})

	t.Run("extra_body that sets both names keeps only the provider field", func(t *testing.T) {
		c := newClient(t, func(cfg *Config) {
			cfg.Settings.MaxTokensField = "max_completion_tokens"
			cfg.Settings.ExtraBody = map[string]any{
				"max_tokens":            1024,
				"max_completion_tokens": 4096,
			}
		})
		got := bodyJSON(t, c, msgs, &api.Options{}, false)
		assert.Equal(t, float64(4096), got["max_completion_tokens"])
		_, hasOther := got["max_tokens"]
		assert.False(t, hasOther, "extra_body sent both token fields, body=%v", got)
	})
}

func TestSkillInstallerDeepSeekBodyHasOneTokenField(t *testing.T) {
	// The installer agent pins thinking off and sends one completion budget
	// (think.go). A provider that wants max_tokens must not also see
	// max_completion_tokens.
	off := false
	c := newClient(t, func(cfg *Config) {
		cfg.Endpoint.Model = "DeepSeek-V4-Flash"
		cfg.Settings.MaxTokensField = "max_tokens"
		cfg.Settings.ThinkingFormat = "thinking-type"
	})
	got := bodyJSON(t, c, []api.Message{{Role: "user", Content: "install"}}, &api.Options{
		Temperature:         0.2,
		MaxCompletionTokens: 24576,
		Thinking:            &off,
	}, true)
	require.Equal(t, float64(24576), got["max_tokens"])
	_, hasOther := got["max_completion_tokens"]
	require.False(t, hasOther, "body=%v", got)
}
