package agent

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestToolExecutionTimeout(t *testing.T) {
	assert.Equal(t, 10*time.Minute+5*time.Second, toolExecutionTimeout("shell_exec"))
	assert.Equal(t, 60*time.Second, toolExecutionTimeout("web_fetch"))
}

func TestGetLLMCallTimeout(t *testing.T) {
	cases := []struct {
		name     string
		config   *types.AgentConfig
		expected time.Duration
	}{
		{"nil config falls back to default", nil, defaultLLMCallTimeout},
		{"zero falls back to default", &types.AgentConfig{LLMCallTimeout: 0}, defaultLLMCallTimeout},
		{"negative falls back to default", &types.AgentConfig{LLMCallTimeout: -5}, defaultLLMCallTimeout},
		{"below minimum clamps up", &types.AgentConfig{LLMCallTimeout: 1}, minLLMCallTimeout},
		{"at minimum passes through", &types.AgentConfig{LLMCallTimeout: 10}, minLLMCallTimeout},
		{"in range passes through", &types.AgentConfig{LLMCallTimeout: 120}, 120 * time.Second},
		{"at maximum passes through", &types.AgentConfig{LLMCallTimeout: 3600}, maxLLMCallTimeout},
		{"above maximum clamps down", &types.AgentConfig{LLMCallTimeout: 99999}, maxLLMCallTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &AgentEngine{config: tc.config}
			assert.Equal(t, tc.expected, e.getLLMCallTimeout())
		})
	}
}
