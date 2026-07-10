package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"
)

// CubeSandbox implements the Sandbox interface backed by a Cube Sandbox
// MicroVM. A single CubeSandbox value wraps a single remote sandbox instance.
//
// Two usage modes are supported:
//
//   - Ephemeral (created via NewCubeSandbox): each Execute call spins up a
//     brand-new MicroVM and tears it down right after — closest to the current
//     Docker/Local backends' semantics.
//   - Persistent (created via newPersistentCubeSandbox): the remote sandbox is
//     created lazily on the first Execute and reused across subsequent calls,
//     preserving installed packages, files, and long-running processes.
//     This is what SessionBoundManager uses to bind one MicroVM per Session.
type CubeSandbox struct {
	config    *Config
	client    *cubeClient
	ephemeral bool // when true, Execute wraps every call in create+kill

	mu       sync.Mutex
	info     *SandboxInfo
	lastUsed time.Time
}

// NewCubeSandbox creates an ephemeral CubeSandbox. Callers may share it
// across many Execute calls, but each Execute isolates itself by provisioning
// its own MicroVM. This matches the stateless contract that DockerSandbox
// and LocalSandbox observe today, so it is safe to plug directly into
// DefaultManager as an alternative backend.
func NewCubeSandbox(config *Config) *CubeSandbox {
	if config == nil {
		config = DefaultConfig()
	}
	applyCubeDefaults(config)
	return &CubeSandbox{
		config:    config,
		client:    newCubeClient(config),
		ephemeral: true,
	}
}

// newPersistentCubeSandbox creates a session-bound sandbox that reuses a
// single MicroVM across calls. This is only used by SessionBoundManager.
func newPersistentCubeSandbox(config *Config, client *cubeClient) *CubeSandbox {
	return &CubeSandbox{
		config:    config,
		client:    client,
		ephemeral: false,
	}
}

// Type returns SandboxTypeCube.
func (s *CubeSandbox) Type() SandboxType { return SandboxTypeCube }

// IsAvailable checks whether the Cube API is reachable.
func (s *CubeSandbox) IsAvailable(ctx context.Context) bool {
	return s.client.Health(ctx) == nil
}

// LastUsed reports the last time Execute was invoked. Callers (notably the
// SessionBoundManager reaper) use it to decide when to pause or kill an idle
// sandbox.
func (s *CubeSandbox) LastUsed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

// RemoteID returns the current remote sandbox ID (or "" when none is bound).
func (s *CubeSandbox) RemoteID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info == nil {
		return ""
	}
	return s.info.ID
}

// Execute runs a script inside the sandbox.
//
// Steps:
//  1. Make sure a remote sandbox exists (lazy-create for persistent mode).
//  2. Upload the script under a stable directory (/home/user/scripts/<hash>/).
//  3. Invoke envd's Process.Start to run <interpreter> <script> <args>.
//  4. Return an ExecuteResult; on ephemeral sandboxes, kill the MicroVM
//     regardless of success.
func (s *CubeSandbox) Execute(ctx context.Context, cfg *ExecuteConfig) (*ExecuteResult, error) {
	if cfg == nil {
		return nil, ErrInvalidScript
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()

	info, err := s.ensureSandbox(execCtx)
	if err != nil {
		return nil, err
	}
	// Ephemeral sandboxes are always torn down after a single Execute.
	if s.ephemeral {
		defer s.disposeEphemeral(info)
	}

	// Upload script content.
	scriptContent, err := readScriptContent(cfg)
	if err != nil {
		return nil, err
	}
	remoteDir := "/workspace"
	remoteScript := path.Join(remoteDir, filepath.Base(cfg.Script))
	if err := s.client.WriteFile(execCtx, info, remoteScript, scriptContent); err != nil {
		return nil, fmt.Errorf("cube sandbox: upload script: %w", err)
	}

	interpreter := getInterpreter(remoteScript)
	// envd runs commands through /bin/sh -lc under the hood; using the plain
	// interpreter name (e.g. "python3") lets $PATH resolve it inside the
	// sandbox image without hard-coding absolute paths.
	args := append([]string{remoteScript}, cfg.Args...)
	cmdResult, err := s.client.RunCommand(
		execCtx,
		info,
		interpreter,
		args,
		cfg.Stdin,
		cfg.Env,
		remoteDir,
	)
	duration := time.Since(startTime)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &ExecuteResult{
				Duration: duration,
				Killed:   true,
				ExitCode: -1,
				Error:    ErrTimeout.Error(),
			}, nil
		}
		return &ExecuteResult{
			Duration: duration,
			ExitCode: -1,
			Error:    err.Error(),
		}, nil
	}

	// Refresh last-used timestamp so the reaper considers this sandbox hot.
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()

	return &ExecuteResult{
		Stdout:   cmdResult.Stdout,
		Stderr:   cmdResult.Stderr,
		ExitCode: cmdResult.ExitCode,
		Duration: duration,
		Killed:   cmdResult.Killed,
	}, nil
}

// Cleanup destroys the remote sandbox if one is bound. Safe to call multiple
// times.
func (s *CubeSandbox) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	info := s.info
	s.info = nil
	s.mu.Unlock()
	if info == nil {
		return nil
	}
	return s.client.killSandboxByInfo(ctx, info)
}

// ensureSandbox lazily provisions a MicroVM.
func (s *CubeSandbox) ensureSandbox(ctx context.Context) (*SandboxInfo, error) {
	s.mu.Lock()
	if s.info != nil {
		info := s.info
		s.mu.Unlock()
		return info, nil
	}
	s.mu.Unlock()

	template := s.config.CubeTemplate
	if template == "" {
		template = DefaultCubeTemplate
	}
	ttl := s.config.CubeSandboxTTL
	if ttl <= 0 {
		ttl = DefaultCubeSandboxTTL
	}
	info, err := s.client.CreateSandbox(ctx, template, ttl)
	if err != nil {
		return nil, fmt.Errorf("cube sandbox: create: %w", err)
	}

	s.mu.Lock()
	if s.info == nil {
		s.info = info
		s.lastUsed = time.Now()
	} else {
		// A concurrent caller beat us; keep theirs and kill ours to avoid
		// leaks. Best-effort.
		go func(dup *SandboxInfo) {
			bg, cancel := context.WithTimeout(context.Background(), DefaultCubeHTTPTimeout)
			defer cancel()
			_ = s.client.killSandboxByInfo(bg, dup)
		}(info)
		info = s.info
	}
	s.mu.Unlock()
	return info, nil
}

// disposeEphemeral tears down an ephemeral MicroVM after Execute completes.
// Errors are swallowed after logging via the standard package logger to keep
// Sandbox.Execute deterministic.
func (s *CubeSandbox) disposeEphemeral(info *SandboxInfo) {
	if info == nil {
		return
	}
	bg, cancel := context.WithTimeout(context.Background(), DefaultCubeHTTPTimeout)
	defer cancel()
	_ = s.client.killSandboxByInfo(bg, info)
	s.mu.Lock()
	if s.info != nil && s.info.ID == info.ID {
		s.info = nil
	}
	s.mu.Unlock()
}

// applyCubeDefaults fills in any missing Cube-related fields in place so that
// downstream code can rely on them being non-zero.
func applyCubeDefaults(cfg *Config) {
	if cfg.CubeAPIURL == "" {
		cfg.CubeAPIURL = DefaultCubeAPIURL
	}
	if cfg.CubeProxyURL == "" {
		cfg.CubeProxyURL = DefaultCubeProxyURL
	}
	if cfg.CubeSandboxDomain == "" {
		cfg.CubeSandboxDomain = DefaultCubeSandboxDomain
	}
	if cfg.CubeEnvdPort <= 0 {
		cfg.CubeEnvdPort = DefaultCubeEnvdPort
	}
	if cfg.CubeTemplate == "" {
		cfg.CubeTemplate = DefaultCubeTemplate
	}
	if cfg.CubeSandboxTTL <= 0 {
		cfg.CubeSandboxTTL = DefaultCubeSandboxTTL
	}
	if cfg.CubeIdleTTL <= 0 {
		cfg.CubeIdleTTL = DefaultCubeIdleTTL
	}
	if cfg.CubeHTTPTimeout <= 0 {
		cfg.CubeHTTPTimeout = DefaultCubeHTTPTimeout
	}
}

// readScriptContent resolves the script bytes to upload into the sandbox.
// It prefers the pre-loaded ScriptContent (set by the security validator),
// falling back to reading the local file at ExecuteConfig.Script.
func readScriptContent(cfg *ExecuteConfig) ([]byte, error) {
	if cfg.ScriptContent != "" {
		return []byte(cfg.ScriptContent), nil
	}
	if cfg.Script == "" {
		return nil, ErrInvalidScript
	}
	content, err := os.ReadFile(cfg.Script)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrScriptNotFound
		}
		return nil, fmt.Errorf("cube sandbox: read script: %w", err)
	}
	return content, nil
}
