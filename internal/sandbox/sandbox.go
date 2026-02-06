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
	// SandboxTypeDisabled means script execution is disabled
	SandboxTypeDisabled SandboxType = "disabled"
)

// Default configuration values
const (
	DefaultTimeout     = 60 * time.Second
	DefaultMemoryLimit = 256 * 1024 * 1024 // 256MB
	DefaultCPULimit    = 1.0               // 1 CPU core
	DefaultDockerImage = "wechatopenai/weknora-sandbox:latest"
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

	// AllowedCmds is a whitelist of commands that can be executed
	// If empty, a default safe list is used
	AllowedCmds []string

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

// GetOutput returns the combined stdout and stderr, preferring stdout
func (r *ExecuteResult) GetOutput() string {
	if r.Stdout != "" {
		return r.Stdout
	}
	return r.Stderr
}

// Config holds sandbox manager configuration
type Config struct {
	// Type is the preferred sandbox type
	Type SandboxType

	// FallbackEnabled allows falling back to local sandbox if Docker is unavailable
	FallbackEnabled bool

	// DefaultTimeout is the default execution timeout
	DefaultTimeout time.Duration

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
}

// DefaultConfig returns a default sandbox configuration
func DefaultConfig() *Config {
	return &Config{
		Type:            SandboxTypeLocal,
		FallbackEnabled: true,
		DefaultTimeout:  DefaultTimeout,
		DockerImage:     DefaultDockerImage,
		AllowedCommands: defaultAllowedCommands(),
		MaxMemory:       DefaultMemoryLimit,
		MaxCPU:          DefaultCPULimit,
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
	case SandboxTypeDocker, SandboxTypeLocal, SandboxTypeDisabled:
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
