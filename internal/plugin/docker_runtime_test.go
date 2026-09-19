package plugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func dockerArgsForTest(req StartPluginRequest) []string {
	controller := NewLocalDockerRuntimeController("", nil)
	return controller.dockerArgs(req, "C:\\plugin-runtime", "/run/weknora/plugin.sock", "weknora-plugin-test")
}

func TestDockerRuntimeSecurityArgs(t *testing.T) {
	req := StartPluginRequest{
		PluginID:            "weknora.example",
		Image:               "example/plugin:dev",
		ProtocolVersion:     ProtocolVersionV1,
		NetworkPolicy:       NetworkAllowlist,
		AllowedDestinations: []string{"api.example.com:443"},
	}
	joined := strings.Join(dockerArgsForTest(req), " ")
	require.Contains(t, joined, "--network none")
	require.Contains(t, joined, "--read-only")
	require.Contains(t, joined, "--cap-drop ALL")
	require.Contains(t, joined, "no-new-privileges")
	require.Contains(t, joined, "--pids-limit 128")
	require.Contains(t, joined, "--memory 512m")
	require.Contains(t, joined, "WEKNORA_PLUGIN_NETWORK_ALLOWLIST=api.example.com:443")
}

func TestDockerRuntimeUsesManagedEgressSocketForNetworkedOCI(t *testing.T) {
	req := StartPluginRequest{
		PluginID:            "weknora.example",
		Image:               "example/plugin:dev",
		ProtocolVersion:     ProtocolVersionV1,
		NetworkPolicy:       NetworkAllowlist,
		AllowedDestinations: []string{"api.example.com:443"},
	}
	joined := strings.Join(dockerArgsForTest(req), " ")
	require.Contains(t, joined, "--network none")
	require.Contains(t, joined, "WEKNORA_PLUGIN_EGRESS_SOCKET=/run/weknora-egress/egress.sock")
	require.Contains(t, joined, "/run/weknora-egress:ro")
	require.Contains(t, joined, "WEKNORA_PLUGIN_NETWORK_POLICY=allowlist")
}

func TestDockerRuntimeDoesNotExposeEgressSocketWhenOffline(t *testing.T) {
	req := StartPluginRequest{
		PluginID:        "weknora.example",
		Image:           "example/plugin:dev",
		ProtocolVersion: ProtocolVersionV1,
		NetworkPolicy:   NetworkNone,
	}
	require.NotContains(t, strings.Join(dockerArgsForTest(req), " "), "WEKNORA_PLUGIN_EGRESS_SOCKET")
}

func TestPluginContainerNameUniquePerInstance(t *testing.T) {
	nameA := pluginContainerName("weknora", "weknora.tarily123")
	nameB := pluginContainerName("other", "weknora.tarily123")
	require.NotEqual(t, nameA, nameB)
	require.Contains(t, nameA, "weknora")
	require.Contains(t, nameB, "other")
}

func TestPluginContainerNameSanitizes(t *testing.T) {
	require.Equal(t, "weknora-plugin-weknora-instance-a.b_c", pluginContainerName("WeKnora-Instance", "a.b_c"))
}
