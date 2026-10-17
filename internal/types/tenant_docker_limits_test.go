package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDockerSandboxConfigHardLimitsJSON(t *testing.T) {
	encoded, err := json.Marshal(DockerSandboxConfig{
		CPUTimeLimitSeconds: 90,
		HardLifetimeSeconds: 3600,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"cpu_time_limit_seconds": 90,
		"hard_lifetime_seconds": 3600
	}`, string(encoded))

	var decoded DockerSandboxConfig
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, 90, decoded.CPUTimeLimitSeconds)
	require.Equal(t, 3600, decoded.HardLifetimeSeconds)
}
