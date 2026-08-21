// Package sandbox: provider-neutral remote Sandbox implementation.
//
// RemoteSandbox is the concrete Sandbox that speaks any RemoteSandboxClient
// backend. It contains no Cube- or E2B-specific logic; every operation is
// expressed against the RemoteSandboxClient contract and a RemoteSandboxHandle.
//
// SessionBoundManager uses it in two shapes:
//
//   - Ephemeral: one call to Execute allocates a sandbox, uploads the script,
//     runs it, then deletes the sandbox regardless of success. This gives
//     stateless-per-call semantics for callers without a SessionID, matching
//     the Local backend.
//   - Persistent (session-bound): SessionBoundManager resolves the session's
//     RemoteSandboxHandle through the lifecycle coordinator and hands it to
//     RemoteSandbox.ExecuteOnHandle. The handle stays owned by the manager;
//     RemoteSandbox never creates or deletes anything in this mode.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// remoteScriptDir is the absolute directory inside every remote sandbox where
// WeKnora uploads scripts before execution. Kept identical to the historical
// Cube path so existing tenant scripts continue to reference /workspace files.
const remoteScriptDir = "/workspace"

// RemoteSandbox implements Sandbox on top of a RemoteSandboxClient. It is
// intentionally stateless: no local mutexes, no cached handles. Higher layers
// decide whether a call allocates a fresh sandbox or reuses an existing one.
type RemoteSandbox struct {
	client        RemoteSandboxClient
	createRequest RemoteCreateRequest
}

// NewRemoteSandbox builds a stateless RemoteSandbox. createRequest is the
// template used for ephemeral Execute calls. Callers that only use
// ExecuteOnHandle may pass a zero createRequest.
func NewRemoteSandbox(client RemoteSandboxClient, createRequest RemoteCreateRequest) *RemoteSandbox {
	return &RemoteSandbox{client: client, createRequest: createRequest}
}

// Type mirrors the underlying provider so callers can gate provider-specific
// behaviour (see agent_service capability registration).
func (s *RemoteSandbox) Type() SandboxType {
	if s == nil || s.client == nil {
		return SandboxTypeDisabled
	}
	return s.client.Provider()
}

// IsAvailable probes the provider's control plane. Returns true only when
// Health succeeds within ctx.
func (s *RemoteSandbox) IsAvailable(ctx context.Context) bool {
	if s == nil || s.client == nil {
		return false
	}
	return s.client.Health(ctx) == nil
}

// Cleanup is a no-op: RemoteSandbox holds no long-lived state. Session
// cleanup lives in SessionBoundManager, which owns the authoritative binding.
func (s *RemoteSandbox) Cleanup(context.Context) error { return nil }

// Execute allocates a fresh sandbox, runs cfg's script, and always tears the
// sandbox down. It is the ephemeral path used when a caller provides no
// SessionID.
func (s *RemoteSandbox) Execute(ctx context.Context, cfg *ExecuteConfig) (*ExecuteResult, error) {
	if s == nil || s.client == nil {
		return nil, ErrSandboxDisabled
	}
	if cfg == nil {
		return nil, ErrInvalidScript
	}
	if s.createRequest.TemplateID == "" {
		return nil, errors.New("sandbox: remote sandbox has no template configured")
	}

	execCtx, cancel := boundedExecuteContext(ctx, cfg)
	defer cancel()

	handle, err := s.client.Create(execCtx, s.createRequest)
	if err != nil {
		return nil, fmt.Errorf("remote sandbox: create: %w", err)
	}
	defer s.disposeEphemeral(ctx, handle)

	return s.ExecuteOnHandle(execCtx, handle, cfg)
}

// ExecuteOnHandle runs cfg's script against an existing handle without
// allocating or deleting the underlying sandbox. It is the persistent path
// SessionBoundManager drives after resolving the session's handle.
func (s *RemoteSandbox) ExecuteOnHandle(
	ctx context.Context,
	handle RemoteSandboxHandle,
	cfg *ExecuteConfig,
) (*ExecuteResult, error) {
	if s == nil || s.client == nil {
		return nil, ErrSandboxDisabled
	}
	if handle == nil {
		return nil, errors.New("sandbox: remote handle is required")
	}
	if cfg == nil {
		return nil, ErrInvalidScript
	}

	content, err := readScriptContent(cfg)
	if err != nil {
		return nil, err
	}
	scriptName := filepath.Base(cfg.Script)
	if scriptName == "" || scriptName == "." || scriptName == "/" {
		return nil, ErrInvalidScript
	}
	remoteScript := path.Join(remoteScriptDir, scriptName)

	if err := s.client.WriteFile(ctx, handle, remoteScript, content); err != nil {
		return nil, fmt.Errorf("remote sandbox: upload script %s: %w", remoteScript, err)
	}

	timeout := effectiveTimeout(cfg, 0)
	request := RemoteExecRequest{
		Command: getInterpreter(remoteScript),
		Args:    append([]string{remoteScript}, cfg.Args...),
		Stdin:   cfg.Stdin,
		Env:     cfg.Env,
		WorkDir: remoteScriptDir,
		User:    DefaultSandboxExecUser,
		Timeout: timeout,
	}

	start := time.Now()
	execResult, err := s.client.Exec(ctx, handle, request)
	duration := time.Since(start)
	return remoteExecuteResult(execResult, err, duration), nil
}

// disposeEphemeral deletes the sandbox after an ephemeral Execute completes.
// Errors are logged inside the client's normalization layer; we swallow them
// so the caller always sees the primary result.
func (s *RemoteSandbox) disposeEphemeral(parent context.Context, handle RemoteSandboxHandle) {
	if handle == nil || handle.ID() == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parent),
		remoteCleanupTimeout,
	)
	defer cancel()
	_ = s.client.Delete(cleanupCtx, handle.ID())
}

// remoteCleanupTimeout bounds the detached delete issued after an ephemeral
// Execute. Matches the manager's session-scoped cleanup budget so behaviour
// stays predictable regardless of which path performs the cleanup.
const remoteCleanupTimeout = 30 * time.Second

// remoteExecuteResult projects RemoteExecResult onto WeKnora's ExecuteResult.
// It preserves the timeout contract: a timeout returns a killed result with
// exit code -1 and ErrTimeout as Error, never an error return.
func remoteExecuteResult(result *RemoteExecResult, err error, duration time.Duration) *ExecuteResult {
	if err != nil {
		if IsRemoteInvalidRequest(err) {
			return &ExecuteResult{
				Duration: duration,
				ExitCode: -1,
				Error:    err.Error(),
			}
		}
		return &ExecuteResult{
			Duration: duration,
			ExitCode: -1,
			Error:    err.Error(),
		}
	}
	if result == nil {
		return &ExecuteResult{
			Duration: duration,
			ExitCode: -1,
			Error:    "sandbox: remote provider returned no result",
		}
	}
	if result.Killed {
		return &ExecuteResult{
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
			Duration: result.Duration,
			Killed:   true,
			ExitCode: -1,
			Error:    ErrTimeout.Error(),
		}
	}
	return &ExecuteResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Duration: result.Duration,
	}
}

// getInterpreter picks the interpreter for a script from its extension. The
// remote backends upload the script and then exec this interpreter against
// the uploaded path, so the choice must not depend on anything host-side.
func getInterpreter(scriptName string) string {
	switch strings.ToLower(filepath.Ext(scriptName)) {
	case ".py":
		return "python3"
	case ".sh", ".bash":
		return "bash"
	case ".js":
		return "node"
	case ".rb":
		return "ruby"
	case ".pl":
		return "perl"
	default:
		return "sh"
	}
}

// readScriptContent resolves the script bytes to upload into the sandbox.
// It prefers cfg.ScriptContent (populated by the security validator) and
// falls back to reading cfg.Script from local disk.
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
		return nil, fmt.Errorf("remote sandbox: read script: %w", err)
	}
	return content, nil
}

// boundedExecuteContext returns a derived context with the effective timeout
// applied. It is only used on the ephemeral path; persistent execution runs
// under SessionBoundManager's context (which the lifecycle lock already
// bounds).
func boundedExecuteContext(parent context.Context, cfg *ExecuteConfig) (context.Context, context.CancelFunc) {
	timeout := effectiveTimeout(cfg, 0)
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func effectiveTimeout(cfg *ExecuteConfig, fallback time.Duration) time.Duration {
	if cfg != nil && cfg.Timeout > 0 {
		return cfg.Timeout
	}
	if fallback > 0 {
		return fallback
	}
	return DefaultTimeout
}
