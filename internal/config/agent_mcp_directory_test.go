package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMCPDirectoryMaxAge(t *testing.T) {
	var unset *AgentConfig
	require.Equal(t, DefaultMCPDirectoryMaxAge, unset.MCPDirectoryMaxAge())
	require.Equal(t, DefaultMCPDirectoryMaxAge, (&AgentConfig{}).MCPDirectoryMaxAge())
	require.Equal(t, 30*time.Minute, (&AgentConfig{MCPDirectoryMaxAgeSeconds: 1800}).MCPDirectoryMaxAge())
	require.Zero(t, (&AgentConfig{MCPDirectoryMaxAgeSeconds: -1}).MCPDirectoryMaxAge(),
		"a negative value turns the age check off")
}

func TestMCPDirectoryMaxAgeEnvOverride(t *testing.T) {
	for value, want := range map[string]int{"30m": 1800, "900": 900, "-1": -1} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WEKNORA_AGENT_MCP_DIRECTORY_MAX_AGE", value)
			cfg := &Config{}
			applyAgentEnvOverrides(cfg)
			require.Equal(t, want, cfg.Agent.MCPDirectoryMaxAgeSeconds)
		})
	}
}
