package sandbox

import (
	"context"
	"fmt"
	"time"
)

// OpenSandboxSandbox implements the Sandbox interface using the external OpenSandbox REST API.
type OpenSandboxSandbox struct {
	config    *Config
	client    *openSandboxClient
	semaphore chan struct{}
}

// NewOpenSandboxSandbox creates a new OpenSandbox-based sandbox.
func NewOpenSandboxSandbox(config *Config) *OpenSandboxSandbox {
	if config == nil {
		config = DefaultConfig()
	}

	var client *openSandboxClient
	if config.OpenSandboxAPIURL != "" {
		client = newOpenSandboxClient(config.OpenSandboxAPIURL, config.OpenSandboxAPIKey)
	}

	return &OpenSandboxSandbox{
		config:    config,
		client:    client,
		semaphore: make(chan struct{}, config.MaxConcurrentSandboxes),
	}
}

// Type returns the sandbox type.
func (s *OpenSandboxSandbox) Type() SandboxType {
	return SandboxTypeOpenSandbox
}

// IsAvailable checks if the OpenSandbox API is reachable.
func (s *OpenSandboxSandbox) IsAvailable(ctx context.Context) bool {
	if s.client == nil {
		return false
	}
	return s.client.healthCheck(ctx)
}

// Execute runs a script via the OpenSandbox REST API.
func (s *OpenSandboxSandbox) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	if config == nil {
		return nil, ErrInvalidScript
	}

	scriptContent, scriptName, err := resolveScript(config)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: %w", err)
	}

	if s.config.MaxScriptSize > 0 && int64(len(scriptContent)) > s.config.MaxScriptSize {
		return nil, fmt.Errorf("opensandbox: script size %d exceeds limit %d", len(scriptContent), s.config.MaxScriptSize)
	}

	select {
	case s.semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.semaphore }()

	timeout := config.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create sandbox
	sbResp, err := s.client.createSandbox(execCtx, s.config.DockerImage, timeout)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: create sandbox: %w", err)
	}

	// Defer sandbox cleanup with a fresh timeout context
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cleanupCancel()
		_ = s.client.deleteSandbox(cleanupCtx, sbResp.ID)
	}()

	// Get execd URL via server proxy endpoint
	execdURL, err := s.client.getExecdURL(execCtx, sbResp.ID)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: %w", err)
	}

	// Wait for execd to become ready
	if err := s.client.waitForExecd(execCtx, execdURL); err != nil {
		return nil, fmt.Errorf("opensandbox: %w", err)
	}

	// Upload script
	if err := s.client.uploadFile(execCtx, execdURL, scriptName, scriptContent); err != nil {
		return nil, fmt.Errorf("opensandbox: upload script: %w", err)
	}

	// Upload stdin content as a file if provided
	if config.Stdin != "" {
		if err := s.client.uploadFile(execCtx, execdURL, stdinFileName, config.Stdin); err != nil {
			return nil, fmt.Errorf("opensandbox: upload stdin: %w", err)
		}
	}

	// Build and execute command
	interpreter := getInterpreter(scriptName)
	command := buildShellCommand(interpreter, "/workspace/"+scriptName, config.Args, config.Stdin != "")

	startTime := time.Now()
	execResp, err := s.client.executeCommand(execCtx, execdURL, command)
	duration := time.Since(startTime)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: execute command: %w", err)
	}

	// Truncate stdout/stderr to MaxLogSize
	stdout := execResp.Stdout
	stderr := execResp.Stderr
	if s.config.MaxLogSize > 0 {
		if int64(len(stdout)) > s.config.MaxLogSize {
			stdout = stdout[:s.config.MaxLogSize]
		}
		if int64(len(stderr)) > s.config.MaxLogSize {
			stderr = stderr[:s.config.MaxLogSize]
		}
	}

	return &ExecuteResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: execResp.ExitCode,
		Duration: duration,
	}, nil
}

// Cleanup releases sandbox resources. OpenSandbox sandboxes are cleaned up per-execution.
func (s *OpenSandboxSandbox) Cleanup(ctx context.Context) error {
	return nil
}
