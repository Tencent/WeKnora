// Package sandbox provides isolated execution environments for running untrusted scripts.
// It supports Docker containers and remote MicroVM backends (CubeSandbox, E2B).
package sandbox

import (
	"context"
	"errors"
	"time"
)

// SandboxType represents the type of sandbox environment
type SandboxType string

const (
	// SandboxTypeDocker runs each session in its own long-lived Docker
	// container, driven through the Docker Engine API. Like the MicroVM
	// backends it keeps session state between executions; unlike them it
	// shares the host kernel and lives on a single daemon.
	SandboxTypeDocker SandboxType = "docker"
	// SandboxTypeCube uses Tencent CubeSandbox (E2B-compatible) MicroVM for isolation.
	// Like Docker and E2B it keeps session-scoped persistent sandboxes: multiple
	// executions bound to the same SessionID share one instance and preserve
	// installed packages, created files, running services, etc.
	SandboxTypeCube SandboxType = "cube"
	// SandboxTypeE2B uses E2B's hosted MicroVM sandbox service.
	SandboxTypeE2B SandboxType = "e2b"
	// SandboxTypeDisabled means script execution is disabled
	SandboxTypeDisabled SandboxType = "disabled"
)

// IsNamedSandboxBackendType reports whether raw can be stored as a user-facing
// named sandbox backend. Cube, E2B and Docker are all session-persistent and
// share the same workspace configuration surface.
func IsNamedSandboxBackendType(raw string) bool {
	switch SandboxType(raw) {
	case SandboxTypeCube, SandboxTypeE2B, SandboxTypeDocker:
		return true
	default:
		return false
	}
}

// Default configuration values
const (
	DefaultTimeout     = 60 * time.Second
	DefaultMemoryLimit = 256 * 1024 * 1024 // 256MB
	DefaultCPULimit    = 1.0               // 1 CPU core
	// DefaultDockerImage tracks main rather than latest. The latest tag only
	// moves when a version is released, so it still carries the image from
	// before /workspace and its input/output directories were handed to the
	// sandbox account — a sandbox built from it cannot write its own artifact
	// directory. Point this back at latest once a release ships that fix.
	DefaultDockerImage = "wechatopenai/weknora-sandbox:main"

	// DefaultCubeTemplateImage is the same environment with Cube's envd daemon
	// baked in (target "cube" of docker/Dockerfile.sandbox).
	//
	// Cube turns an OCI image into a template directly and gates the build on
	// GET :49983/health, which only envd answers. Building a Cube template from
	// DefaultDockerImage therefore always fails the probe with "connection
	// refused" — E2B gets away with that image because its own builder injects
	// envd, and the Docker backend never needs one.
	DefaultCubeTemplateImage = "wechatopenai/weknora-sandbox:main-cube"

	// CubeEnvdPort is the port envd listens on inside a Cube sandbox. It carries
	// the readiness probe as well as every exec and filesystem call, and the
	// data plane addresses sandboxes as "49983-{id}.{domain}".
	CubeEnvdPort = 49983

	// CubeEnvdHealthPath is the envd endpoint Cube probes to decide whether a
	// template build succeeded.
	CubeEnvdHealthPath = "/health"

	// DefaultCubeAPIURL is retained for SDK tests and explicit local helpers;
	// workspace configs must still provide their endpoint.
	DefaultCubeAPIURL = "http://127.0.0.1:33000"
	// DefaultCubeProxyURL is the default CubeProxy endpoint (HTTP, port 80) used
	// to reach the in-sandbox envd via host-header routing.
	DefaultCubeProxyURL = "http://127.0.0.1:80"
	// DefaultCubeSandboxDomain is the sandbox routing domain configured on
	// CubeProxy (matches CUBE_API_SANDBOX_DOMAIN in the Cube deployment).
	DefaultCubeSandboxDomain = "cube.app"
	// DefaultCubeSandboxTTL is the Cube-side sandbox lifetime hint (in seconds)
	// requested at creation; the sandbox is torn down by CubeMaster if the
	// client goes silent for longer than this value.
	DefaultCubeSandboxTTL = 30 * time.Minute
	// DefaultCubeHTTPTimeout bounds a single HTTP call to the CubeAPI
	// (excluding user script execution which has its own per-call timeout).
	DefaultCubeHTTPTimeout = 30 * time.Second

	// DefaultE2BSandboxTTL matches the E2B SDK's built-in default so an
	// unset E2BSandboxTTL still yields a valid sandbox lifetime.
	DefaultE2BSandboxTTL = 5 * time.Minute
	// DefaultE2BHTTPTimeout bounds a single HTTP call to the E2B API.
	DefaultE2BHTTPTimeout = 30 * time.Second
)

// Common errors
var (
	ErrSandboxDisabled   = errors.New("sandbox is disabled")
	ErrTimeout           = errors.New("execution timed out")
	ErrScriptNotFound    = errors.New("script not found")
	ErrInvalidScript     = errors.New("invalid script")
	ErrExecutionFailed   = errors.New("script execution failed")
	ErrSecurityViolation = errors.New("security validation failed")
	ErrDangerousCommand  = errors.New("script contains dangerous command")
	ErrArgInjection      = errors.New("argument injection detected")
	ErrStdinInjection    = errors.New("stdin injection detected")
)

// Sandbox defines the interface for isolated script execution
type Sandbox interface {
	// Execute runs a script in an isolated environment
	Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error)

	// Cleanup releases sandbox resources
	Cleanup(ctx context.Context) error

	// Type returns the sandbox type
	Type() SandboxType

	// IsAvailable checks if the sandbox is available for use
	IsAvailable(ctx context.Context) bool
}

// Manager provides a unified interface for sandbox operations
// It handles sandbox selection and fallback logic
type Manager interface {
	// Execute runs a script using the configured sandbox
	Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error)

	// Cleanup releases all sandbox resources
	Cleanup(ctx context.Context) error

	// GetSandbox returns the active sandbox
	GetSandbox() Sandbox

	// GetType returns the current sandbox type
	GetType() SandboxType
}

// ExecuteConfig contains configuration for script execution
type ExecuteConfig struct {
	// Script is the absolute path to the script file
	Script string

	// Args are command-line arguments to pass to the script
	Args []string

	// WorkDir is the working directory for script execution
	WorkDir string

	// Timeout is the maximum execution time (0 = use default)
	Timeout time.Duration

	// Env is additional environment variables
	Env map[string]string

	// AllowNetwork enables network access (Docker only)
	AllowNetwork bool

	// MemoryLimit is the maximum memory in bytes (Docker only)
	MemoryLimit int64

	// CPULimit is the maximum CPU cores (Docker only)
	CPULimit float64

	// ReadOnlyRootfs makes the root filesystem read-only (Docker only)
	ReadOnlyRootfs bool

	// Stdin provides input to the script
	Stdin string

	// SkipValidation skips security validation (use with caution, only for trusted scripts)
	SkipValidation bool

	// ScriptContent is the script content for validation (optional, will be read from file if not provided)
	ScriptContent string

	// SessionID scopes the execution to a per-session persistent sandbox.
	// Honoured by Cube, E2B and Docker. When empty, those backends fall back
	// to an ephemeral (one-shot) sandbox created and torn down inside the
	// single Execute call.
	SessionID string

	// RemoteScriptPath is an absolute path to a script that already exists
	// inside the sandbox image (installed skills). When set, the executor
	// skips the upload step and runs it in place. Only paths under a valid
	// skill directory in SkillsImageRoot are accepted; script-content
	// validation is skipped because the file is already on the image, so
	// callers must have vetted the bundle at install time.
	RemoteScriptPath string
}

// ExecuteResult contains the result of script execution
type ExecuteResult struct {
	// Stdout is the standard output from the script
	Stdout string

	// Stderr is the standard error from the script
	Stderr string

	// ExitCode is the process exit code
	ExitCode int

	// Duration is the actual execution time
	Duration time.Duration

	// Killed indicates if the process was killed (e.g., timeout)
	Killed bool

	// Error contains any execution error
	Error string
}

// IsSuccess returns true if the script executed successfully
func (r *ExecuteResult) IsSuccess() bool {
	return r.ExitCode == 0 && !r.Killed && r.Error == ""
}

// Config holds sandbox manager configuration
type Config struct {
	// Type is the preferred sandbox type
	Type SandboxType

	// DefaultTimeout is the default execution timeout
	DefaultTimeout time.Duration

	// AllowPrivateEndpoints is the per-workspace outbound policy for this
	// connection. Link-local addresses are blocked regardless.
	AllowPrivateEndpoints bool

	// DockerImage is the image every sandbox container is created from. It
	// plays the same role as a Cube/E2B template ID.
	DockerImage string

	// DockerHost is the daemon endpoint, in DOCKER_HOST form
	// ("unix:///var/run/docker.sock", "tcp://10.0.0.5:2376"). Empty uses
	// DefaultDockerHost.
	DockerHost string

	// DockerTLSCertPath is a directory on the WeKnora host holding
	// ca.pem / cert.pem / key.pem. Required for a TCP daemon; unix sockets
	// do not use TLS.
	DockerTLSCertPath string

	// DockerCPULimit / DockerMemoryBytes / DockerPidsLimit cap one sandbox
	// container. Zero uses the built-in defaults.
	DockerCPULimit    float64
	DockerMemoryBytes int64
	DockerPidsLimit   int64

	// DockerNetworkMode is the Docker network every sandbox joins: "bridge" or
	// "none". host, container: and named networks are rejected (see
	// ValidateDockerNetworkMode). Empty means "bridge"; skills that install
	// packages need egress, so a sandbox is not isolated from the network by
	// default.
	DockerNetworkMode string

	// DockerRuntime selects an alternative OCI runtime, e.g. "runsc" for
	// gVisor. Empty uses the daemon's default runtime.
	DockerRuntime string

	// DockerIdleTTL is how long a container may go without executing anything
	// before the idle sweep reclaims it. Zero uses DefaultDockerIdleTTL.
	DockerIdleTTL time.Duration

	// DockerHTTPTimeout bounds each Engine API call. Zero uses the default.
	DockerHTTPTimeout time.Duration

	// MaxMemory is the maximum memory limit in bytes
	MaxMemory int64

	// MaxCPU is the maximum CPU cores
	MaxCPU float64

	// EnvVars are additional environment variables to set for the sandbox.
	EnvVars map[string]string

	// CubeAPIURL is the base URL of the CubeAPI (E2B-compatible) endpoint.
	// Only used when Type == SandboxTypeCube. Example: "http://127.0.0.1:33000".
	CubeAPIURL string

	// CubeProxyURL is the base URL of the CubeProxy HTTP endpoint through which
	// in-sandbox envd traffic is routed via host-header rewriting. Example:
	// "http://127.0.0.1:80".
	CubeProxyURL string

	// CubeSandboxDomain matches CubeAPI's CUBE_API_SANDBOX_DOMAIN. It is used to
	// build the Host header "<port>-<sandboxID>.<domain>" that CubeProxy relies
	// on to route requests into the correct MicroVM.
	CubeSandboxDomain string

	// CubeAPIKey is the API key sent via X-API-Key. Leave empty when the Cube
	// deployment does not enforce authentication.
	CubeAPIKey string

	// CubeTemplate is the default template ID used when creating sandboxes.
	CubeTemplate string

	// CubeSandboxTTL is the Cube-side lifetime hint (passed as `timeout` when
	// creating a sandbox). CubeMaster will reap the MicroVM if the client stops
	// touching it for longer than this duration.
	CubeSandboxTTL time.Duration

	// CubeHTTPTimeout bounds each HTTP call to CubeAPI. Zero uses the default.
	CubeHTTPTimeout time.Duration

	// CubeDNSServers are nameserver IPs included when WeKnora builds the
	// standard Cube template. Empty omits the field so Cubelet uses its
	// cluster default.
	CubeDNSServers []string

	// E2BAPIKey is the E2B API key sent via X-API-Key. Only used when
	// Type == SandboxTypeE2B.
	E2BAPIKey string

	// E2BAPIURL is the E2B control-plane endpoint. Empty defaults to
	E2BAPIURL string

	// E2BSandboxDomain is the domain envd traffic is routed through, e.g.
	// "e2b.app". Empty defaults to the SDK's built-in.
	E2BSandboxDomain string

	// E2BProxyURL is the data-plane gateway that fronts envd for self-hosted
	// E2B-compatible control planes. Empty keeps the SDK's behaviour of
	// resolving the sandbox authority through DNS over TLS, which is what E2B
	// Cloud expects. See types.E2BSandboxConfig.ProxyURL.
	E2BProxyURL string

	// E2BTemplate is the E2B template ID used at sandbox creation.
	E2BTemplate string

	// E2BSandboxTTL is the E2B-side idle timeout hint.
	E2BSandboxTTL time.Duration

	// E2BHTTPTimeout bounds each HTTP call to the E2B API.
	E2BHTTPTimeout time.Duration
}

// DefaultConfig returns a default sandbox configuration.
//
// It deliberately carries no Cube or E2B endpoint, credential or template:
// those belong to a named workspace config. Presetting them here once meant an
// incomplete workspace config could silently dial localhost.
func DefaultConfig() *Config {
	return &Config{
		Type:            SandboxTypeDisabled,
		DefaultTimeout:  DefaultTimeout,
		DockerImage:     DefaultDockerImage,
		MaxMemory:       DefaultMemoryLimit,
		MaxCPU:          DefaultCPULimit,
		CubeSandboxTTL:  DefaultCubeSandboxTTL,
		CubeHTTPTimeout: DefaultCubeHTTPTimeout,
	}
}

// ValidateConfig validates sandbox configuration
func ValidateConfig(config *Config) error {
	if config == nil {
		return errors.New("config is nil")
	}

	switch config.Type {
	case SandboxTypeDocker, SandboxTypeCube, SandboxTypeE2B, SandboxTypeDisabled:
		// Valid types
	default:
		return errors.New("invalid sandbox type")
	}

	if config.DefaultTimeout < 0 {
		return errors.New("timeout cannot be negative")
	}

	if config.MaxMemory < 0 {
		return errors.New("memory limit cannot be negative")
	}

	if config.MaxCPU < 0 {
		return errors.New("CPU limit cannot be negative")
	}

	return nil
}
