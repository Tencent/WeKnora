package sandbox

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
)

// DefaultManager implements the Manager interface
// It handles sandbox selection and fallback logic
type DefaultManager struct {
	config    *Config
	sandbox   Sandbox
	validator *ScriptValidator
	mu        sync.RWMutex
}

// NewManager creates a new sandbox manager with the given configuration
func NewManager(config *Config) (Manager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid sandbox config: %w", err)
	}

	manager := &DefaultManager{
		config:    config,
		validator: NewScriptValidator(),
	}

	// Initialize the appropriate sandbox
	if err := manager.initializeSandbox(context.Background()); err != nil {
		return nil, err
	}

	return manager, nil
}

// initializeSandbox creates and configures the sandbox based on configuration
func (m *DefaultManager) initializeSandbox(ctx context.Context) error {
	switch m.config.Type {
	case SandboxTypeDisabled:
		m.sandbox = &disabledSandbox{}
		return nil

	case SandboxTypeDocker:
		dockerSandbox := NewDockerSandbox(m.config)
		if dockerSandbox.IsAvailable(ctx) {
			m.sandbox = dockerSandbox
			// Pre-pull the sandbox image asynchronously so it's ready before first use
			go func() {
				if err := dockerSandbox.EnsureImage(context.Background()); err != nil {
					log.Printf("[sandbox] failed to pre-pull image %s: %v", m.config.DockerImage, err)
				} else {
					log.Printf("[sandbox] image %s is ready", m.config.DockerImage)
				}
			}()
			return nil
		}

		// Fallback to local if enabled
		if m.config.FallbackEnabled {
			m.sandbox = NewLocalSandbox(m.config)
			return nil
		}

		return fmt.Errorf("docker is not available and fallback is disabled")

	case SandboxTypeLocal:
		m.sandbox = NewLocalSandbox(m.config)
		return nil

	default:
		return fmt.Errorf("unknown sandbox type: %s", m.config.Type)
	}
}

// Execute runs a script using the configured sandbox
// It performs security validation before execution to prevent prompt injection attacks
func (m *DefaultManager) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	m.mu.RLock()
	sandbox := m.sandbox
	m.mu.RUnlock()

	if sandbox == nil {
		return nil, ErrSandboxDisabled
	}

	// Check if sandbox is disabled - return early without validation
	if sandbox.Type() == SandboxTypeDisabled {
		return nil, ErrSandboxDisabled
	}

	// Perform security validation unless explicitly skipped
	if !config.SkipValidation {
		if err := m.validateExecution(config); err != nil {
			log.Printf("[sandbox] Security validation failed: %v", err)
			return &ExecuteResult{
				ExitCode: -1,
				Error:    err.Error(),
				Stderr:   fmt.Sprintf("Security validation failed: %v", err),
			}, ErrSecurityViolation
		}
	}

	return sandbox.Execute(ctx, config)
}

// validateExecution performs comprehensive security validation on the execution config
func (m *DefaultManager) validateExecution(config *ExecuteConfig) error {
	if m.validator == nil {
		return nil
	}

	// Get script content for validation
	scriptContent := config.ScriptContent
	if scriptContent == "" && config.Script != "" {
		content, err := os.ReadFile(config.Script)
		if err != nil {
			return fmt.Errorf("failed to read script for validation: %w", err)
		}
		scriptContent = string(content)
	}

	// Validate script content
	if scriptContent != "" {
		result := m.validator.ValidateScript(scriptContent)
		if !result.Valid {
			// Log all validation errors
			for _, verr := range result.Errors {
				log.Printf("[sandbox] Validation error: %s", verr.Error())
			}
			// Return the first error
			if len(result.Errors) > 0 {
				return result.Errors[0]
			}
			return ErrSecurityViolation
		}
	}

	// Validate arguments
	if len(config.Args) > 0 {
		result := m.validator.ValidateArgs(config.Args)
		if !result.Valid {
			for _, verr := range result.Errors {
				log.Printf("[sandbox] Arg validation error: %s", verr.Error())
			}
			if len(result.Errors) > 0 {
				return result.Errors[0]
			}
			return ErrArgInjection
		}
	}

	// Validate stdin
	if config.Stdin != "" {
		result := m.validator.ValidateStdin(config.Stdin)
		if !result.Valid {
			for _, verr := range result.Errors {
				log.Printf("[sandbox] Stdin validation error: %s", verr.Error())
			}
			if len(result.Errors) > 0 {
				return result.Errors[0]
			}
			return ErrStdinInjection
		}
	}

	return nil
}

// Cleanup releases all sandbox resources
func (m *DefaultManager) Cleanup(ctx context.Context) error {
	m.mu.RLock()
	sandbox := m.sandbox
	m.mu.RUnlock()

	if sandbox != nil {
		return sandbox.Cleanup(ctx)
	}
	return nil
}

// GetSandbox returns the active sandbox
func (m *DefaultManager) GetSandbox() Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sandbox
}

// GetType returns the current sandbox type
func (m *DefaultManager) GetType() SandboxType {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sandbox != nil {
		return m.sandbox.Type()
	}
	return SandboxTypeDisabled
}

// disabledSandbox is a no-op sandbox that rejects all execution requests
type disabledSandbox struct{}

func (s *disabledSandbox) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	return nil, ErrSandboxDisabled
}

func (s *disabledSandbox) Cleanup(ctx context.Context) error {
	return nil
}

func (s *disabledSandbox) Type() SandboxType {
	return SandboxTypeDisabled
}

func (s *disabledSandbox) IsAvailable(ctx context.Context) bool {
	return false
}

// NewManagerFromType creates a sandbox manager with the specified type.
// dockerImage is optional; if empty, the default image is used.
func NewManagerFromType(sandboxType string, fallbackEnabled bool, dockerImage string) (Manager, error) {
	var sType SandboxType
	switch sandboxType {
	case "docker":
		sType = SandboxTypeDocker
	case "local":
		sType = SandboxTypeLocal
	case "disabled", "":
		sType = SandboxTypeDisabled
	default:
		return nil, fmt.Errorf("unknown sandbox type: %s", sandboxType)
	}

	config := DefaultConfig()
	config.Type = sType
	config.FallbackEnabled = fallbackEnabled
	if dockerImage != "" {
		config.DockerImage = dockerImage
	}

	return NewManager(config)
}

// NewDisabledManager creates a manager that rejects all execution requests
func NewDisabledManager() Manager {
	return &DefaultManager{
		config:    DefaultConfig(),
		sandbox:   &disabledSandbox{},
		validator: NewScriptValidator(),
	}
}
