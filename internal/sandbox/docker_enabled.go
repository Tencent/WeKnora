package sandbox

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// DockerBackendEnabledEnv is the process-level fallback for the Docker
// sandbox backend. System Settings (sandbox.docker_enabled) override it
// when a row has been pushed by SystemSettingService.
//
// A workspace admin who can save a Docker config can create containers on
// whatever Engine API this process can reach — typically the host's
// docker.sock, which is host root. It is therefore off until a
// SystemAdmin or deployer opts in.
const DockerBackendEnabledEnv = "WEKNORA_SANDBOX_DOCKER_ENABLED"

// DockerBackendEnabledSettingKey is the system_settings registry key.
const DockerBackendEnabledSettingKey = "sandbox.docker_enabled"

// ErrDockerBackendDisabled is returned when a Docker sandbox config is saved,
// probed, or resolved and the process has not opted in.
var ErrDockerBackendDisabled = errors.New(
	"sandbox: docker backend is disabled; enable it in System Settings or set WEKNORA_SANDBOX_DOCKER_ENABLED=true",
)

// dockerBackendEnabledOverride is the runtime-tunable source. Nil means
// "SystemSettingService has not pushed yet"; DockerBackendEnabled then
// reads the env, matching the preload window and tests that only Setenv.
var dockerBackendEnabledOverride atomic.Pointer[bool]

// SetDockerBackendEnabled records the resolved 3-tier value (DB > env >
// false). Called at system_settings preload, Update, Reset, and pubsub reload.
func SetDockerBackendEnabled(enabled bool) {
	v := enabled
	dockerBackendEnabledOverride.Store(&v)
}

// ClearDockerBackendEnabledOverride restores env-only resolution. Tests that
// construct SystemSettingService can otherwise leak a preload push into later
// Setenv-based cases in the same package.
func ClearDockerBackendEnabledOverride() {
	dockerBackendEnabledOverride.Store(nil)
}

// DockerBackendEnabled reports whether this process may run the Docker sandbox
// backend. Empty or unparsable env values are false.
func DockerBackendEnabled() bool {
	if p := dockerBackendEnabledOverride.Load(); p != nil {
		return *p
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(DockerBackendEnabledEnv)))
	return err == nil && parsed
}

// EnsureDockerBackendAllowed is the choke point for every path that would
// talk to a Docker daemon on behalf of a workspace config.
func EnsureDockerBackendAllowed(t SandboxType) error {
	if t != SandboxTypeDocker {
		return nil
	}
	if DockerBackendEnabled() {
		return nil
	}
	return ErrDockerBackendDisabled
}
