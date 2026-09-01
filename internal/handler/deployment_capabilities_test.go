package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

func TestDeploymentCapabilityKeysMatchFrontend(t *testing.T) {
	frontendKeys, err := readFrontendDeploymentCapabilityKeys()
	if err != nil {
		t.Fatalf("read frontend capability keys: %v", err)
	}

	if !slices.Equal(DeploymentCapabilityKeys, frontendKeys) {
		t.Fatalf("backend keys = %#v, frontend keys = %#v", DeploymentCapabilityKeys, frontendKeys)
	}
}

func TestBuildDeploymentCapabilitiesIncludesAllKeys(t *testing.T) {
	result := BuildDeploymentCapabilities("standard", DeploymentFeatureAvailability{
		Organizations: true,
		Agents:        true,
		IM:            true,
		Embed:         true,
		API:           true,
		MCP:           true,
		WebSearch:     true,
		VectorStore:   true,
		Storage:       true,
		Sandbox:       true,
	})

	for _, key := range DeploymentCapabilityKeys {
		if _, ok := result.Capabilities[key]; !ok {
			t.Fatalf("missing capability key %q", key)
		}
	}
}

func TestOverlayLiveDockerSandboxCapabilityIgnoresStartupSnapshot(t *testing.T) {
	sandbox.ClearDockerBackendEnabledOverride()
	t.Cleanup(sandbox.ClearDockerBackendEnabledOverride)
	t.Setenv(sandbox.DockerBackendEnabledEnv, "")

	snapshot := BuildDeploymentCapabilities("standard", DeploymentFeatureAvailability{
		Sandbox:       true,
		SandboxDocker: true,
	})
	live := overlayLiveDockerSandboxCapability(snapshot)
	docker := live.Capabilities["settings.sandbox.docker"]
	if docker.Supported {
		t.Fatal("live env off must hide docker even if the startup snapshot was on")
	}
	if docker.Reason != "docker_backend_disabled" {
		t.Fatalf("reason = %q, want docker_backend_disabled", docker.Reason)
	}

	t.Setenv(sandbox.DockerBackendEnabledEnv, "true")
	enabled := overlayLiveDockerSandboxCapability(snapshot)
	if !enabled.Capabilities["settings.sandbox.docker"].Supported {
		t.Fatal("live env true must expose docker")
	}
}

func readFrontendDeploymentCapabilityKeys() ([]string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, os.ErrInvalid
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	frontendPath := filepath.Join(repoRoot, "frontend", "src", "config", "deploymentCapabilities.ts")
	content, err := os.ReadFile(frontendPath)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(`(?s)export const DEPLOYMENT_CAPABILITY_KEYS = \[(.*?)\]`)
	match := re.FindSubmatch(content)
	if len(match) < 2 {
		return nil, os.ErrInvalid
	}

	var keys []string
	for _, line := range strings.Split(string(match[1]), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, ","))
		if line == "" {
			continue
		}
		line = strings.Trim(line, `'`)
		keys = append(keys, line)
	}
	return keys, nil
}
