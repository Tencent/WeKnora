package adapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// InstallRunner is the slice of localsandbox.Service an install needs.
type InstallRunner interface {
	RunWithPolicy(
		ctx context.Context, policy localsandbox.Policy, req localsandbox.RunRequest,
	) (*localsandbox.RunResult, error)
}

// InstallPolicies builds the install-only policy for one version directory.
type InstallPolicies interface {
	BuildInstall(dir string) (localsandbox.Policy, error)
}

// InstallManager is the sandbox the built-in skill installer agent talks to on
// Lite. Each maintenance session is bound to one version directory; an
// unbound session has no scope and every call fails.
//
// It deliberately does not implement SessionCapabilityProvider: chat agents
// must never resolve to a shell whose network is open.
type InstallManager struct {
	runner   InstallRunner
	policies InstallPolicies

	mu     sync.Mutex
	scopes map[string]string
}

var (
	_ sandbox.Manager                          = (*InstallManager)(nil)
	_ sandbox.SessionInstallCapabilityProvider = (*InstallManager)(nil)
	_ sandbox.SessionInstallShellExecutor      = (*InstallManager)(nil)
	_ sandbox.SessionFileReader                = (*InstallManager)(nil)
)

var errNoInstallScope = errors.New("localsandbox: this session is not a skill install")

// NewInstallManager wires the install sandbox.
func NewInstallManager(runner InstallRunner, policies InstallPolicies) *InstallManager {
	return &InstallManager{runner: runner, policies: policies, scopes: map[string]string{}}
}

// Bind scopes sessionID to dir until release is called.
func (m *InstallManager) Bind(sessionID, dir string) func() {
	m.mu.Lock()
	m.scopes[sessionID] = filepath.Clean(dir)
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.scopes, sessionID)
		m.mu.Unlock()
	}
}

func (m *InstallManager) scope(sessionID string) (string, localsandbox.Policy, error) {
	m.mu.Lock()
	dir, ok := m.scopes[sessionID]
	m.mu.Unlock()
	if !ok {
		return "", localsandbox.Policy{}, errNoInstallScope
	}
	policy, err := m.policies.BuildInstall(dir)
	if err != nil {
		return "", localsandbox.Policy{}, err
	}
	return dir, policy, nil
}

// GetType reports host so logs and type switches read naturally.
func (m *InstallManager) GetType() sandbox.SandboxType { return sandbox.SandboxTypeHost }

// GetSandbox is unused: there is no remote instance.
func (m *InstallManager) GetSandbox() sandbox.Sandbox { return nil }

// Cleanup holds nothing process-wide.
func (m *InstallManager) Cleanup(context.Context) error { return nil }

// Execute is the legacy script entry point, which installs never use.
func (m *InstallManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return nil, fmt.Errorf("localsandbox: script execution is not supported; use shell_exec")
}

// SessionInstallShellExecutor exposes the install shell.
func (m *InstallManager) SessionInstallShellExecutor() sandbox.SessionInstallShellExecutor { return m }

// ExecShellCommandWithOptions runs one installer command in the bound
// version directory. AsRoot and AllowSkillsRoot have no meaning here: the
// command runs as the desktop user and may write only that directory.
func (m *InstallManager) ExecShellCommandWithOptions(
	ctx context.Context, sessionID, command string, opts sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	dir, policy, err := m.scope(sessionID)
	if err != nil {
		return nil, err
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == sandbox.SessionWorkspaceRoot {
		workDir = ""
	}
	res, err := m.runner.RunWithPolicy(ctx, policy, localsandbox.RunRequest{
		SessionID: sessionID,
		Command:   command,
		WorkDir:   workDir,
		Timeout:   opts.Timeout,
		Env:       installEnv(dir, opts.Env),
	})
	if err != nil {
		logger.Warnf(ctx, "[LocalSandbox] install exec session=%s dir=%s: %v", sessionID, dir, err)
		return nil, err
	}
	return executeResult(ctx, sessionID, res), nil
}

// installEnv points package caches inside dir: the default cache locations
// under the home directory are not writable to the install policy.
func installEnv(dir string, extra map[string]string) map[string]string {
	cache := filepath.Join(dir, ".weknora", "cache")
	env := make(map[string]string, len(extra)+4)
	for k, v := range extra {
		env[k] = v
	}
	env["UV_CACHE_DIR"] = filepath.Join(cache, "uv")
	env["PIP_CACHE_DIR"] = filepath.Join(cache, "pip")
	env["npm_config_cache"] = filepath.Join(cache, "npm")
	env["WEKNORA_SKILL_DIR"] = dir
	return env
}

func (m *InstallManager) guard(sessionID string) (*localsandbox.PathGuard, string, error) {
	dir, policy, err := m.scope(sessionID)
	if err != nil {
		return nil, "", err
	}
	return localsandbox.NewPathGuard(policy), dir, nil
}

// StatSessionFile returns metadata for p inside the version directory.
func (m *InstallManager) StatSessionFile(
	_ context.Context, sessionID, p string,
) (*sandbox.RemoteStatEntry, error) {
	guard, dir, err := m.guard(sessionID)
	if err != nil {
		return nil, err
	}
	target := absoluteIn(dir, p)
	info, err := guard.Lstat(target)
	if err != nil {
		return nil, err
	}
	entryType := sandbox.RemoteEntryFile
	if info.IsDir() {
		entryType = sandbox.RemoteEntryDir
	}
	return &sandbox.RemoteStatEntry{Path: target, Type: entryType, Size: info.Size(), ModTime: info.ModTime()}, nil
}

// ReadSessionFile reads p inside the version directory.
func (m *InstallManager) ReadSessionFile(_ context.Context, sessionID, p string) ([]byte, error) {
	guard, dir, err := m.guard(sessionID)
	if err != nil {
		return nil, err
	}
	return guard.ReadFile(absoluteIn(dir, p))
}

// WriteSessionFile writes p inside the version directory, creating parents.
func (m *InstallManager) WriteSessionFile(_ context.Context, sessionID, p string, content []byte) error {
	guard, dir, err := m.guard(sessionID)
	if err != nil {
		return err
	}
	target := absoluteIn(dir, p)
	if err := guard.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return guard.WriteFile(target, content, 0o644)
}
