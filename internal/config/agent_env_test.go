// Package config tests configuration environment overrides.
package config

import "testing"

func TestApplyAgentEnvOverridesWebFetchToolTimeout(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "duration", env: "3m", want: 180},
		{name: "bare seconds", env: "240", want: 240},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEKNORA_AGENT_LLM_TIMEOUT", "")
			t.Setenv("WEKNORA_AGENT_TOOL_APPROVAL_TIMEOUT", "")
			t.Setenv("WEKNORA_WEB_FETCH_TOOL_TIMEOUT", tc.env)
			cfg := &Config{}

			applyAgentEnvOverrides(cfg)

			if cfg.Agent.WebFetchToolTimeout != tc.want {
				t.Fatalf("web_fetch_tool_timeout = %d, want %d", cfg.Agent.WebFetchToolTimeout, tc.want)
			}
		})
	}
}
