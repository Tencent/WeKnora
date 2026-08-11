package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// stubSandboxResolver stands in for the per-config resolver. Its presence is
// what makes the collector take the resolving branch at all.
type stubSandboxResolver struct {
	mgr sandbox.Manager
	err error
}

func (s stubSandboxResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return s.mgr, s.err
}

// artifactFallbackManager is a minimal sandbox.Manager that delegates artifact
// reads to a SandboxArtifactSource — used when tests exercise sentinel pins.
type artifactFallbackManager struct {
	source SandboxArtifactSource
}

func (m *artifactFallbackManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	panic("unexpected Execute")
}
func (m *artifactFallbackManager) Cleanup(context.Context) error { return nil }
func (m *artifactFallbackManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *artifactFallbackManager) GetType() sandbox.SandboxType {
	return sandbox.SandboxTypeCube
}

func (m *artifactFallbackManager) ListSessionFiles(ctx context.Context, sessionID, dir string) ([]sandbox.RemoteDirEntry, error) {
	return m.source.ListSessionFiles(ctx, sessionID, dir)
}
func (m *artifactFallbackManager) ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return m.source.ReadSessionFile(ctx, sessionID, path)
}

// Execution decides the config; every later operation must follow the pin, so
// that re-pointing the agent mid-conversation cannot split a session across
// two backends.
func TestSandboxConfigForExecutionPinsAgentChoice(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := sandboxConfigForExecution(ctx, pinner, "s-1", "cfg-a")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", got)

	// Admin re-points the agent at cfg-b: the live sandbox stays on cfg-a.
	got, err = sandboxConfigForExecution(ctx, pinner, "s-1", "cfg-b")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", got)
}

func TestSandboxConfigForExistingSandboxReturnsEmptyWhenUnpinned(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))

	got, err := sandboxConfigForExistingSandbox(context.Background(), pinner, "s-1")
	require.NoError(t, err)
	require.Empty(t, got, "no pin means no live sandbox; callers must skip")
}

func TestSandboxConfigForExecutionUsesSentinelForGlobalDefault(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))

	got, err := sandboxConfigForExecution(context.Background(), pinner, "s-1", "")
	require.NoError(t, err)
	require.Equal(t, types.SandboxConfigIDGlobalDefault, got)
}

// Attachment staging can be the call that creates the session's first sandbox
// (WriteSessionInputFile provisions on first write), so it must pin like
// execution does - not follow a pin that does not exist yet.
func TestSandboxConfigForExecutionPinsOnFirstStaging(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	staged, err := sandboxConfigForExecution(ctx, pinner, "s-1", "cfg-a")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", staged)

	// The execution that follows in the same turn must land on the same config.
	executed, err := sandboxConfigForExecution(ctx, pinner, "s-1", "cfg-a")
	require.NoError(t, err)
	require.Equal(t, staged, executed)
}

// Most deployments run every session on the WEKNORA_SANDBOX_* default, whose
// pin is the sentinel and whose manager is the injected process-wide one. That
// must keep collecting artifacts: resolving the sentinel yields no per-config
// manager, which is not the same as "this session has no sandbox".
func TestArtifactSessionSourceKeepsDefaultBackendForSentinelPin(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_MODE", "cube")

	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()
	_, err := pinner.Pin(ctx, "s-1", types.SandboxConfigIDGlobalDefault)
	require.NoError(t, err)

	source := &fakeSandboxSource{}
	collector := &ArtifactCollector{
		source:      source,
		resolver:    stubSandboxResolver{},
		pinner:      pinner,
		fallbackMgr: &artifactFallbackManager{source: source},
	}

	got := collector.sessionSource(ctx, "s-1")
	require.NotNil(t, got)
	// Sentinel pins resolve to the deployment-wide manager, which may be a
	// distinct object from c.source even though both read the same backend.
	_, ok := got.(SandboxArtifactSource)
	require.True(t, ok)
}

// Attachment staging is reached through a runtime type assertion in
// session_agent_qa.go, so a signature drift there does not fail the build - it
// makes every agent turn error out with "does not support session attachment
// staging". This pins the shape that call site asserts.
func TestAgentServiceSatisfiesStagingAssertion(t *testing.T) {
	var svc any = &agentService{}
	_, ok := svc.(sessionAttachmentStager)
	require.True(t, ok)
}

// An unpinned session has no live sandbox, so there is nothing to read even
// though a process-wide source exists.
func TestArtifactSessionSourceSkipsUnpinnedSession(t *testing.T) {
	collector := &ArtifactCollector{
		source:   &fakeSandboxSource{},
		resolver: stubSandboxResolver{},
		pinner:   NewSessionSandboxPinner(newPinTestDB(t)),
	}

	require.Nil(t, collector.sessionSource(context.Background(), "s-1"))
}
