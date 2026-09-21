package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
)

// capableManager is a test double that lets each case declare which
// session-scoped capabilities the sandbox manager currently advertises.
// It mirrors the real SessionCapabilityProvider contract SessionBoundManager
// implements, without pulling in the full manager wiring.
type capableManager struct {
	typ          sandbox.SandboxType
	shell        sandbox.SessionShellExecutor
	files        sandbox.SessionFileStore
	installShell sandbox.SessionInstallShellExecutor
}

func (m *capableManager) SessionInstallShellExecutor() sandbox.SessionInstallShellExecutor {
	return m.installShell
}

func (m *capableManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return &sandbox.ExecuteResult{}, nil
}
func (m *capableManager) Cleanup(context.Context) error { return nil }
func (m *capableManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *capableManager) GetType() sandbox.SandboxType  { return m.typ }
func (m *capableManager) SessionShellExecutor() sandbox.SessionShellExecutor {
	return m.shell
}
func (m *capableManager) SessionFileStore() sandbox.SessionFileStore {
	return m.files
}

// stubShellExecutor records ExecShellCommand calls so a test can assert the
// registered tool actually dispatches through it.
type stubShellExecutor struct{ called bool }

func (s *stubShellExecutor) ExecShellCommand(
	context.Context, string, string, string, time.Duration, map[string]string,
) (*sandbox.ExecuteResult, error) {
	s.called = true
	return &sandbox.ExecuteResult{}, nil
}

func TestSessionSandboxShellExecutorReturnsNilWithoutCapability(t *testing.T) {
	// Managers that don't implement SessionCapabilityProvider (Disabled
	// DefaultManager) must never surface shell_exec.
	nonCapable := &capableManager{typ: sandbox.SandboxTypeDisabled}
	assert.Nil(t, sessionSandboxShellExecutor(nonCapable))
	assert.Nil(t, sessionSandboxFileStore(nonCapable))
	assert.Nil(t, sessionSandboxShellExecutor(nil))
}

func TestSessionSandboxShellExecutorReturnsNilWhenProviderRefuses(t *testing.T) {
	// A provider that advertises capabilities but is currently unable to
	// honour them returns nil from the accessor. The tool layer must respect that.
	m := &capableManager{typ: sandbox.SandboxTypeCube}
	assert.Nil(t, sessionSandboxShellExecutor(m))
	assert.Nil(t, sessionSandboxFileStore(m))
}

func TestSessionSandboxShellExecutorReturnsCapability(t *testing.T) {
	exec := &stubShellExecutor{}
	m := &capableManager{typ: sandbox.SandboxTypeCube, shell: exec}
	got := sessionSandboxShellExecutor(m)
	assert.NotNil(t, got)
	if _, err := got.ExecShellCommand(context.Background(), "sid", "echo", "", 0, nil); err != nil {
		t.Fatalf("dispatch through capability: %v", err)
	}
	assert.True(t, exec.called)
}
