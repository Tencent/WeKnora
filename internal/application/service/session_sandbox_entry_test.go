package service

import (
	"context"
	"fmt"
	"testing"
	"time"

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

func (m *artifactFallbackManager) ListSessionFiles(
	ctx context.Context, sessionID, dir string,
) ([]sandbox.RemoteDirEntry, error) {
	return m.source.ListSessionFiles(ctx, sessionID, dir)
}

func (m *artifactFallbackManager) ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return m.source.ReadSessionFile(ctx, sessionID, path)
}

func TestSandboxConfigForExistingSandboxReturnsEmptyWhenUnpinned(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))

	got, err := sandboxConfigForExistingSandbox(context.Background(), pinner, "s-1")
	require.NoError(t, err)
	require.Empty(t, got, "no pin means no live sandbox; callers must skip")
}

// Older sessions can still carry the historical default-config sentinel. The
// fallback manager remains readable so those sessions can be cleaned up.
func TestArtifactSessionSourceKeepsDefaultBackendForSentinelPin(t *testing.T) {
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
	require.Same(t, source, got)
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

func TestArtifactBaselineTreatsUnpinnedFirstTurnAsEmpty(t *testing.T) {
	collector := &ArtifactCollector{
		source:   &fakeSandboxSource{},
		resolver: stubSandboxResolver{},
		pinner:   NewSessionSandboxPinner(newPinTestDB(t)),
	}

	baseline, err := collector.CaptureTurnBaseline(
		context.Background(), "s-1", "/workspace/output",
	)
	require.NoError(t, err)
	require.True(t, baseline.valid)
	require.Empty(t, baseline.files)
}

// Docker (and other named backends) pin the workspace config on first
// execution. Collection must follow that pin rather than treating the
// session as having no sandbox.
func TestArtifactSessionSourceResolvesNamedPin(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	_, err := pinner.Pin(ctx, "s-1", "cfg-docker")
	require.NoError(t, err)

	named := &artifactFallbackManager{source: &fakeSandboxSource{}}
	collector := &ArtifactCollector{
		source:   &fakeSandboxSource{},
		resolver: stubSandboxResolver{mgr: named},
		pinner:   pinner,
	}

	got := collector.sessionSource(ctx, "s-1")
	require.Equal(t, named, got)
}

type artifactTenantResolver struct {
	manager sandbox.Manager
	tenants []uint64
}

func (r *artifactTenantResolver) Resolve(
	_ context.Context, tenantID uint64, configID string,
) (sandbox.Manager, error) {
	r.tenants = append(r.tenants, tenantID)
	if tenantID != 9 || configID != "shared-agent-config" {
		return nil, fmt.Errorf("config does not belong to tenant %d", tenantID)
	}
	return r.manager, nil
}

type artifactRemoteClient struct {
	sandbox.RemoteSandboxClient
	source   fakeSandboxSource
	created  int
	executed int
	connects []string
}

func (*artifactRemoteClient) ID() string                       { return "shared-session-sandbox" }
func (*artifactRemoteClient) Provider() sandbox.RemoteProvider { return sandbox.SandboxTypeCube }
func (*artifactRemoteClient) Metadata() map[string]string      { return nil }

func (*artifactRemoteClient) Capabilities() sandbox.RemoteSandboxCapabilities {
	return sandbox.RemoteSandboxCapabilities{SupportsReconnect: true, SupportsFilesystemEnumeration: true}
}

func (c *artifactRemoteClient) Create(
	context.Context, sandbox.RemoteCreateRequest,
) (sandbox.RemoteSandboxHandle, error) {
	c.created++
	return c, nil
}

func (c *artifactRemoteClient) Connect(
	_ context.Context, req sandbox.RemoteConnectRequest,
) (sandbox.RemoteSandboxHandle, error) {
	c.connects = append(c.connects, req.SandboxID)
	return c, nil
}

func (c *artifactRemoteClient) Get(context.Context, string) (*sandbox.RemoteSandboxSummary, error) {
	return &sandbox.RemoteSandboxSummary{ID: c.ID(), State: sandbox.RemoteStateRunning}, nil
}

func (*artifactRemoteClient) WriteFile(context.Context, sandbox.RemoteSandboxHandle, string, []byte) error {
	return nil
}

func (c *artifactRemoteClient) Exec(
	_ context.Context, _ sandbox.RemoteSandboxHandle, req sandbox.RemoteExecRequest,
) (*sandbox.RemoteExecResult, error) {
	if !req.Shell {
		c.executed++
		name := fmt.Sprintf("result-%d.txt", c.executed)
		filePath := sandbox.SessionOutputRoot + "/" + name
		c.source.entries["s-1"] = append(c.source.entries["s-1"], sandbox.RemoteDirEntry{
			Name: name, Path: filePath, Type: sandbox.RemoteEntryFile, Size: 6, ModTime: time.Now().UTC(),
		})
		c.source.contents[filePath] = []byte("result")
	}
	return &sandbox.RemoteExecResult{}, nil
}

func (c *artifactRemoteClient) ListDir(
	ctx context.Context, _ sandbox.RemoteSandboxHandle, dir string,
) ([]sandbox.RemoteDirEntry, error) {
	return c.source.ListSessionFiles(ctx, "s-1", dir)
}

func (*artifactRemoteClient) Stat(
	_ context.Context, _ sandbox.RemoteSandboxHandle, filePath string,
) (*sandbox.RemoteStatEntry, error) {
	return &sandbox.RemoteStatEntry{Path: filePath, Type: sandbox.RemoteEntryDir}, nil
}

func (c *artifactRemoteClient) ReadFile(
	ctx context.Context, _ sandbox.RemoteSandboxHandle, filePath string,
) ([]byte, error) {
	return c.source.ReadSessionFile(ctx, "s-1", filePath)
}

func TestArtifactCollectionKeepsSharedAgentTenantScopes(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))
	ctx = types.WithSandboxTenantID(ctx, 7)
	db := newPinTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	pinner := NewSessionSandboxPinner(db)
	bindings := sandbox.NewMemorySessionSandboxBindingStore()
	client := &artifactRemoteClient{source: fakeSandboxSource{
		entries: make(map[string][]sandbox.RemoteDirEntry), contents: make(map[string][]byte),
	}}
	cfg := sandbox.DefaultConfig()
	cfg.CubeTemplate = "artifact-test"
	manager, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: bindings, Checker: sandbox.PermissiveSessionExistenceChecker{},
		ConfigID: "shared-agent-config", SkipHealthProbe: true,
	})
	require.NoError(t, err)
	resolver := &artifactTenantResolver{manager: manager}
	files := &fakeFileService{}
	collector := NewArtifactCollectorFromSandboxManager(nil, resolver, pinner, files, nil, nil)

	// Execute through the production resolver, pinner, manager and binding store.
	execute := func() {
		t.Helper()
		mgr, configID, resolveErr := resolveSandboxForExecution(
			ctx, resolver, nil, pinner, 9, "s-1", "shared-agent-config", nil,
		)
		require.NoError(t, resolveErr)
		require.Equal(t, "shared-agent-config", configID)
		_, execErr := mgr.Execute(ctx, &sandbox.ExecuteConfig{
			SessionID: "s-1", Script: "artifact.py", ScriptContent: "print('result')", SkipValidation: true,
		})
		require.NoError(t, execErr)
	}
	execute()
	baseline, err := collector.CaptureTurnBaseline(ctx, "s-1", sandbox.SessionOutputRoot)
	require.NoError(t, err)
	require.Len(t, baseline.files, 1)
	execute()
	collectCtx := WithArtifactTurnBaseline(context.WithoutCancel(ctx), baseline)
	notified := 0
	artifacts, err := collector.CollectWithNotify(
		collectCtx, "s-1", "message", 7, sandbox.SessionOutputRoot, func(count int) { notified = count },
	)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, "result-2.txt", artifacts[0].FileName)
	require.Equal(t, 1, notified)
	require.Equal(t, []uint64{7}, files.tenantIDs)
	require.Equal(t, []uint64{9, 9, 9, 9}, resolver.tenants)
	require.Equal(t, 1, client.created, "baseline and collection must not allocate another sandbox")
	for _, id := range client.connects {
		require.Equal(t, client.ID(), id)
	}
	ownerBinding, err := bindings.Get(ctx, sandbox.SessionSandboxKey{TenantID: 7, SessionID: "s-1"})
	require.NoError(t, err)
	require.NotNil(t, ownerBinding)
	require.Equal(t, client.ID(), ownerBinding.SandboxID)
	resourceBinding, err := bindings.Get(ctx, sandbox.SessionSandboxKey{TenantID: 9, SessionID: "s-1"})
	require.NoError(t, err)
	require.Nil(t, resourceBinding, "resource tenant must not replace the session's binding tenant")
}
