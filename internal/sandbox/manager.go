package sandbox

import (
	"context"
	"fmt"
	"sync"
)

// DefaultManager implements the Manager interface
// It handles sandbox selection and fallback logic
type DefaultManager struct {
	config  *Config
	sandbox Sandbox
	mu      sync.RWMutex
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
		config: config,
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
func (m *DefaultManager) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	m.mu.RLock()
	sandbox := m.sandbox
	m.mu.RUnlock()

	if sandbox == nil {
		return nil, ErrSandboxDisabled
	}

	return sandbox.Execute(ctx, config)
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

// NewManagerFromType creates a sandbox manager with the specified type
func NewManagerFromType(sandboxType string, fallbackEnabled bool) (Manager, error) {
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

	return NewManager(config)
}

// NewDisabledManager creates a manager that rejects all execution requests
func NewDisabledManager() Manager {
	return &DefaultManager{
		config:  DefaultConfig(),
		sandbox: &disabledSandbox{},
	}
}
