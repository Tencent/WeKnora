// Package sandbox provides isolated execution environments for running untrusted scripts.
// It supports multiple backends including Docker containers and local process isolation.
package sandbox

import (
	"context"
	"errors"
	"time"
)

// SandboxType represents the type of sandbox environment
type SandboxType string

const (
	// SandboxTypeDocker uses Docker containers for isolation
	SandboxTypeDocker SandboxType = "docker"
	// SandboxTypeLocal uses local process with restrictions
	SandboxTypeLocal SandboxType = "local"
	// SandboxTypeCube uses Tencent CubeSandbox (E2B-compatible) MicroVM for isolation.
	// Unlike Docker/Local backends which are stateless per execution, Cube supports
	// session-scoped persistent sandboxes: multiple executions bound to the same
	// SessionID share the same MicroVM instance and preserve installed packages,
	// created files, running services, etc.
	SandboxTypeCube SandboxType = "cube"
	// SandboxTypeE2B uses E2B's hosted MicroVM sandbox service.
	SandboxTypeE2B SandboxType = "e2b"
	// SandboxTypeDisabled means script execution is disabled
	SandboxTypeDisabled SandboxType = "disabled"
)

// IsNamedSandboxBackendType reports whether raw can be stored as a user-facing
// named sandbox backend. Remote backends are session-persistent; docker/local
// are stateless, but all four share the same workspace configuration surface.
func IsNamedSandboxBackendType(raw string) bool {
	switch SandboxType(raw) {
	case SandboxTypeCube, SandboxTypeE2B, SandboxTypeDocker, SandboxTypeLocal:
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
	DefaultDockerImage = "wechatopenai/weknora-sandbox:latest"

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
	// Currently only honoured by remote backends; Docker/Local backends ignore it.
	// When empty, Cube falls back to an ephemeral (one-shot) sandbox that is
	// created and torn down inside the single Execute call.
	SessionID string
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

	// FallbackEnabled allows falling back to local sandbox if Docker is unavailable
	FallbackEnabled bool

	// DefaultTimeout is the default execution timeout
	DefaultTimeout time.Duration

	// AllowPrivateEndpoints is the per-workspace outbound policy for this
	// connection. Link-local addresses are blocked regardless.
	AllowPrivateEndpoints bool

	// DockerImage is the Docker image to use (Docker sandbox only)
	DockerImage string

	// AllowedCommands is the default list of allowed commands
	AllowedCommands []string

	// AllowedPaths is the list of paths that can be accessed
	AllowedPaths []string

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

	// E2BAPIKey is the E2B API key sent via X-API-Key. Only used when
	// Type == SandboxTypeE2B.
	E2BAPIKey string

	// E2BAPIURL is the E2B control-plane endpoint. Empty defaults to
	E2BAPIURL string

	// E2BSandboxDomain is the domain envd traffic is routed through, e.g.
	// "e2b.app". Empty defaults to the SDK's built-in.
	E2BSandboxDomain string

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
		Type:            SandboxTypeLocal,
		FallbackEnabled: true,
		DefaultTimeout:  DefaultTimeout,
		DockerImage:     DefaultDockerImage,
		AllowedCommands: defaultAllowedCommands(),
		MaxMemory:       DefaultMemoryLimit,
		MaxCPU:          DefaultCPULimit,
		CubeSandboxTTL:  DefaultCubeSandboxTTL,
		CubeHTTPTimeout: DefaultCubeHTTPTimeout,
	}
}

// defaultAllowedCommands returns the default list of safe commands
func defaultAllowedCommands() []string {
	return []string{
		"python",
		"python3",
		"node",
		"bash",
		"sh",
		"cat",
		"echo",
		"head",
		"tail",
		"grep",
		"sed",
		"awk",
		"sort",
		"uniq",
		"wc",
		"cut",
		"tr",
		"ls",
		"pwd",
		"date",
	}
}

// ValidateConfig validates sandbox configuration
func ValidateConfig(config *Config) error {
	if config == nil {
		return errors.New("config is nil")
	}

	switch config.Type {
	case SandboxTypeDocker, SandboxTypeLocal, SandboxTypeCube, SandboxTypeE2B, SandboxTypeDisabled:
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
