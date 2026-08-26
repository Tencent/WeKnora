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

	case SandboxTypeCube, SandboxTypeE2B, SandboxTypeDocker:
		// Session-scoped remote backends are only reachable through
		// SessionBoundManager, which owns the authoritative binding.
		// DefaultManager exposes stateless semantics that cannot preserve
		// per-session state, so we refuse the construction and let
		// NewManagerFromType route the caller to NewSessionBoundManager.
		return fmt.Errorf(
			"sandbox: %s backend must be constructed via NewSessionBoundManager",
			m.config.Type,
		)

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

	effective := config
	if config != nil && len(m.config.EnvVars) > 0 {
		copy := *config
		copy.Env = cloneMetadata(m.config.EnvVars)
		for key, value := range config.Env {
			copy.Env[key] = value
		}
		effective = &copy
	}

	// Perform security validation unless explicitly skipped
	if effective != nil && !effective.SkipValidation {
		if err := runScriptValidation(m.validator, effective); err != nil {
			log.Printf("[sandbox] Security validation failed: %v", err)
			return &ExecuteResult{
				ExitCode: -1,
				Error:    err.Error(),
				Stderr:   fmt.Sprintf("Security validation failed: %v", err),
			}, ErrSecurityViolation
		}
	}

	return sandbox.Execute(ctx, effective)
}

// runScriptValidation is the package-level helper that DefaultManager and
// SessionBoundManager share for pre-execution security checks. Extracting
// it avoids duplicating the same script/args/stdin validation logic across
// two Manager implementations while keeping the ScriptValidator private to
// the manager that owns it.
func runScriptValidation(validator *ScriptValidator, config *ExecuteConfig) error {
	if validator == nil || config == nil {
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
		result := validator.ValidateScript(scriptContent)
		if !result.Valid {
			for _, verr := range result.Errors {
				log.Printf("[sandbox] Validation error: %s", verr.Error())
			}
			if len(result.Errors) > 0 {
				return result.Errors[0]
			}
			return ErrSecurityViolation
		}
	}

	// Validate arguments
	if len(config.Args) > 0 {
		result := validator.ValidateArgs(config.Args)
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
		result := validator.ValidateStdin(config.Stdin)
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
//
// Session-scoped backends (Cube, E2B, Docker) route to SessionBoundManager,
// which keeps one persistent sandbox per SessionID; Disabled routes to
// DefaultManager. Both satisfy Manager.
func NewManagerFromType(sandboxType string, dockerImage string) (Manager, error) {
	var sType SandboxType
	switch sandboxType {
	case "docker":
		sType = SandboxTypeDocker
	case "cube":
		sType = SandboxTypeCube
	case "e2b":
		sType = SandboxTypeE2B
	case "disabled", "":
		sType = SandboxTypeDisabled
	default:
		return nil, fmt.Errorf("unknown sandbox type: %s", sandboxType)
	}

	config := DefaultConfig()
	config.Type = sType
	if dockerImage != "" {
		config.DockerImage = dockerImage
	}

	var client RemoteSandboxClient
	var err error
	switch sType {
	case SandboxTypeCube:
		if client, err = NewCubeRemoteClient(config); err != nil {
			return nil, fmt.Errorf("sandbox: build Cube client: %w", err)
		}
	case SandboxTypeE2B:
		if client, err = NewE2BRemoteClient(config); err != nil {
			return nil, fmt.Errorf("sandbox: build E2B client: %w", err)
		}
	case SandboxTypeDocker:
		applyDockerRuntimeDefaults(config)
		if client, err = NewDockerRemoteClient(config); err != nil {
			return nil, fmt.Errorf("sandbox: build Docker client: %w", err)
		}
	}
	if client == nil {
		return NewManager(config)
	}
	return NewSessionBoundManager(SessionBoundManagerConfig{
		Config:  config,
		Client:  client,
		Store:   NewMemorySessionSandboxBindingStore(),
		Checker: PermissiveSessionExistenceChecker{},
	})
}

// NewDisabledManager creates a manager that rejects all execution requests
func NewDisabledManager() Manager {
	return &DefaultManager{
		config:    DefaultConfig(),
		sandbox:   &disabledSandbox{},
		validator: NewScriptValidator(),
	}
}
