package plugin

import (
	"os"
	"strings"
)

// newOCIRuntime constructs the OCI runtime for a plugin according to the
// deployment shape. When a runtime-agent socket is configured the Docker
// privileges stay in the agent (production, app container has no docker.sock);
// otherwise the docker CLI runs in-process (dev / single-process).
func newOCIRuntime(manifest Manifest, image string) Runtime {
	agentSocket := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_RUNTIME_AGENT_SOCKET"))
	if agentSocket == "" {
		return NewDockerRuntime(manifest, image)
	}
	controller := NewRemoteRuntimeAgentController(
		agentSocket,
		os.Getenv("WEKNORA_PLUGIN_RUNTIME_AGENT_TOKEN"),
		os.Getenv("WEKNORA_PLUGIN_RUNTIME_ROOT"),
		os.Getenv("WEKNORA_PLUGIN_RUNTIME_LOCAL_ROOT"),
	)
	return NewDockerRuntimeWithController(manifest, image, controller)
}
