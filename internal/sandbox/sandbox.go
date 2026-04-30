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
	// SandboxTypeE2B uses E2B cloud sandbox for isolation
	SandboxTypeE2B SandboxType = "e2b"
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

	// ExecuteInWorkspace runs a script in a workspace
	ExecuteInWorkspace(ctx context.Context, config *WorkspaceExecuteConfig) (*ExecuteResult, error)

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

	// OutputFiles specifies output files to collect (glob pattern)
	// Scripts can write artifacts to the out/ directory, which will be automatically collected
	// If empty, all files in the out/ directory will be collected by default
	OutputFiles []string

	// CollectOutputDir whether to automatically collect artifacts from the out/ directory
	// Defaults to true, scripts can write results to the $OUTPUT_DIR environment variable
	CollectOutputDir bool
}

// WorkspaceExecuteConfig workspace mode execution configuration
type WorkspaceExecuteConfig struct {
	// SkillName is the name of the skill to execute
	SkillName string

	// SkillSourceDir is the absolute path to the skill source directory
	SkillSourceDir string

	// Command is the command to execute (via bash -c)
	// Command and Script are mutually exclusive, with Command taking precedence
	Command string

	// Script is the script file path (relative to skill directory)
	// Used when Command is empty
	Script string

	// Args are the command-line arguments to pass to the script
	Args []string

	// Cwd is the working directory (relative to skill in workspace)
	Cwd string

	// Stdin is the standard input to pass to the script
	Stdin string

	// Env are additional environment variables
	Env map[string]string

	// Timeout is the maximum execution time
	Timeout time.Duration

	// OutputFiles specifies output files to collect (glob pattern)
	OutputFiles []string

	// WorkspaceRoot is the workspace root directory (empty uses system temp directory)
	WorkspaceRoot string

	// PersistWorkspace whether to keep the workspace (for debugging)
	PersistWorkspace bool

	// AllowNetwork whether to allow network access (Docker mode)
	AllowNetwork bool

	// MemoryLimit memory limit (Docker mode)
	MemoryLimit int64

	// CPULimit CPU limit (Docker mode)
	CPULimit float64
}

// WorkspaceExecutor workspace mode executor interface
type WorkspaceExecutor interface {
	// ExecuteInWorkspace executes a command in an isolated workspace
	ExecuteInWorkspace(ctx context.Context, config *WorkspaceExecuteConfig) (*ExecuteResult, error)
}

// OutputFile means output file
type OutputFile struct {
	// Name is the relative path name (relative to out/ directory)
	Name string `json:"name"`
	// Content is the text content of the file (text files only)
	Content string `json:"content,omitempty"`
	// Data is the raw byte data (binary files)
	Data []byte `json:"-"`
	// MIMEType is the MIME type of the file
	MIMEType string `json:"mime_type,omitempty"`
	// SizeBytes is the file size
	SizeBytes int64 `json:"size_bytes"`
	// IsText indicates whether the file is a text file
	IsText bool `json:"is_text"`
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

	// OutputFiles is the list of output files
	OutputFiles []OutputFile `json:"output_files,omitempty"`
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

	// E2BAPIKey is the E2B API key (falls back to E2B_API_KEY env var)
	E2BAPIKey string

	// E2BTemplate is the E2B sandbox template id/alias (default: code-interpreter-v1)
	E2BTemplate string

	// E2BDomain overrides the E2B domain (default: e2b.app)
	E2BDomain string

	// E2BSandboxTimeout is the wall-clock lifetime of the E2B sandbox
	E2BSandboxTimeout time.Duration
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
		"pip",
		"pip3",
		"node",
		"npm",
		"npx",
		"bash",
		"sh",
		"ruby",
		"perl",
		"php",
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
		"mkdir",
		"cp",
		"mv",
		"find",
		"xargs",
		"tee",
	}
}

// ValidateConfig validates sandbox configuration
func ValidateConfig(config *Config) error {
	if config == nil {
		return errors.New("config is nil")
	}

	switch config.Type {
	case SandboxTypeDocker, SandboxTypeLocal, SandboxTypeE2B, SandboxTypeDisabled:
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
