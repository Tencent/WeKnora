package service

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Only the expected operations are implemented. Accidentally adding lifecycle
// Get/Create calls to lookup-only paths fails rather than hiding extra RPCs.
type operationRequestClient struct {
	sandbox.RemoteSandboxClient
	ops   []string
	files map[string][]byte
	stat  *sandbox.RemoteStatEntry
	exec  sandbox.RemoteExecRequest
}

type operationRequestHandle struct{ id string }

func (h operationRequestHandle) ID() string                    { return h.id }
func (h operationRequestHandle) Provider() sandbox.SandboxType { return sandbox.SandboxTypeCube }
func (h operationRequestHandle) Metadata() map[string]string   { return nil }

func (c *operationRequestClient) Provider() sandbox.SandboxType { return sandbox.SandboxTypeCube }
func (c *operationRequestClient) Capabilities() sandbox.RemoteSandboxCapabilities {
	return sandbox.RemoteSandboxCapabilities{
		SupportsReconnect: true, SupportsMetadata: true, SupportsListSandboxes: true,
		SupportsFilesystemEnumeration: true,
	}
}

func (c *operationRequestClient) Connect(
	_ context.Context, req sandbox.RemoteConnectRequest,
) (sandbox.RemoteSandboxHandle, error) {
	c.ops = append(c.ops, "connect")
	return operationRequestHandle{req.SandboxID}, nil
}

func (c *operationRequestClient) Stat(
	_ context.Context, _ sandbox.RemoteSandboxHandle, path string,
) (*sandbox.RemoteStatEntry, error) {
	c.ops = append(c.ops, "stat")
	if c.stat != nil {
		return c.stat, nil
	}
	return &sandbox.RemoteStatEntry{Path: path, Type: sandbox.RemoteEntryFile, Size: int64(len(c.files[path]))}, nil
}

func (c *operationRequestClient) ReadFile(
	_ context.Context, _ sandbox.RemoteSandboxHandle, path string,
) ([]byte, error) {
	c.ops = append(c.ops, "read")
	return c.files[path], nil
}

func (c *operationRequestClient) ListDir(
	_ context.Context, _ sandbox.RemoteSandboxHandle, _ string,
) ([]sandbox.RemoteDirEntry, error) {
	c.ops = append(c.ops, "list")
	entries := make([]sandbox.RemoteDirEntry, 0, len(c.files))
	for filePath, data := range c.files {
		entries = append(entries, sandbox.RemoteDirEntry{
			Name: path.Base(filePath), Path: filePath, Type: sandbox.RemoteEntryFile, Size: int64(len(data)),
		})
	}
	return entries, nil
}

func (c *operationRequestClient) Exec(
	_ context.Context, _ sandbox.RemoteSandboxHandle, req sandbox.RemoteExecRequest,
) (*sandbox.RemoteExecResult, error) {
	c.ops = append(c.ops, "exec")
	c.exec = req
	return &sandbox.RemoteExecResult{Stdout: strings.Repeat("a", 40) + "\n"}, nil
}

type operationSessionChecker struct{}

func (operationSessionChecker) SessionExists(context.Context, sandbox.SessionSandboxKey) (bool, error) {
	return true, nil
}

func newOperationRequestManager(t *testing.T) (context.Context, *sandbox.SessionBoundManager, *operationRequestClient) {
	t.Helper()
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	store := sandbox.NewMemorySessionSandboxBindingStore()
	_, err := store.Create(ctx, sandbox.SessionSandboxKey{TenantID: 42, SessionID: "s1"}, sandbox.SessionSandboxBinding{
		Version: sandbox.SessionSandboxBindingVersion, Provider: sandbox.SandboxTypeCube,
		TenantID: 42, SessionID: "s1", SandboxID: "sb1", TemplateID: "template", CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	client := &operationRequestClient{files: map[string][]byte{
		"/workspace/output/a.txt": []byte("first"),
		"/workspace/output/b.txt": []byte("second"),
	}}
	cfg := sandbox.DefaultConfig()
	cfg.CubeTemplate = "template"
	mgr, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: store, Checker: operationSessionChecker{}, SkipHealthProbe: true,
	})
	require.NoError(t, err)
	return ctx, mgr, client
}

func TestReadFileReusesConnectionButRefreshesNextCall(t *testing.T) {
	ctx, mgr, client := newOperationRequestManager(t)
	ctx = tools.WithToolExecContext(ctx, &tools.ToolExecContext{SessionID: "s1"})
	reader := tools.NewReadFileTool(mgr)
	result, err := reader.Execute(ctx, json.RawMessage(`{"path":"output/a.txt"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, "first")
	require.Equal(t, []string{"connect", "stat", "read"}, client.ops)

	client.ops = nil
	result, err = reader.Execute(ctx, json.RawMessage(`{"path":"output/a.txt"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, []string{"connect", "stat"}, client.ops,
		"page cache avoids downloads, but each call still checks the live sandbox")

	client.ops = nil
	client.stat = &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: 9 << 20}
	result, err = reader.Execute(ctx, json.RawMessage(`{"path":"output/b.txt"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, []string{"connect", "stat"}, client.ops, "oversize files must not be downloaded")
}

func TestArtifactCollectorUsesOneConnectionForAllFiles(t *testing.T) {
	ctx, mgr, client := newOperationRequestManager(t)
	collector := NewArtifactCollector(mgr, &fakeFileService{}, &fakeStore{}, nil, ArtifactCollectorConfig{})
	artifacts, err := collector.Collect(ctx, "s1", "m1", 42, "/workspace/output")
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	require.Equal(t, []string{"connect", "list", "read", "read"}, client.ops)
	client.ops = nil
	_, err = collector.Collect(ctx, "s1", "m2", 42, "/workspace/output")
	require.NoError(t, err)
	require.Equal(t, []string{"connect", "list", "read", "read"}, client.ops,
		"a new collection must not reuse an old handle")
}

func TestPinnedCheckpointSkipsWorkspacePreparation(t *testing.T) {
	ctx, mgr, client := newOperationRequestManager(t)
	pinned := NewPinnedSessionSandbox(stubPinReader{configID: "cfg1"}, &stubTenantSandboxResolver{mgr: mgr}, nil)
	checkpoint := NewWorkspaceCheckpointer(pinned).Checkpoint(ctx, "s1", "sb1", "m1")
	require.NotNil(t, checkpoint)
	require.Equal(t, "sb1", checkpoint.SandboxID)
	require.Equal(t, strings.Repeat("a", 40), checkpoint.CommitSHA)
	require.Equal(t, []string{"connect", "exec"}, client.ops,
		"SkipWorkspacePrep must omit the extra prepareSessionDirs exec")
	require.Equal(t, workspaceCheckpointTimeout, client.exec.Timeout)
	require.Equal(t, sandbox.SessionWorkspaceRoot, client.exec.WorkDir)
	require.Contains(t, client.exec.Command, "--allow-empty")
	require.Contains(t, client.exec.Command, sandbox.SessionGitDir)
	require.Contains(t, client.exec.Command, `mkdir -p "$WORK_TREE"`)
	require.NotContains(t, client.exec.Command, `mkdir -p -- "$d"`,
		"checkpoint must not run the session input/output bootstrap")
}

func TestPinnedRewindResetSkipsWorkspacePreparation(t *testing.T) {
	ctx, mgr, client := newOperationRequestManager(t)
	pinned := NewPinnedSessionSandbox(stubPinReader{configID: "cfg1"}, &stubTenantSandboxResolver{mgr: mgr}, nil)
	sha := strings.Repeat("a", 40)
	require.NoError(t, resetWorkspaceToCommit(ctx, pinned, "s1", sha, "sb1"))
	require.Equal(t, []string{"connect", "exec"}, client.ops,
		"SkipWorkspacePrep must omit the extra prepareSessionDirs exec")
	require.Equal(t, workspaceResetTimeout, client.exec.Timeout)
	require.Equal(t, sandbox.SessionWorkspaceRoot, client.exec.WorkDir)
	require.Contains(t, client.exec.Command, "reset --hard "+sha)
	require.NotContains(t, client.exec.Command, `mkdir -p -- "$d"`)
}

func TestPinnedEmptyResetSkipsWorkspacePreparation(t *testing.T) {
	ctx, mgr, client := newOperationRequestManager(t)
	pinned := NewPinnedSessionSandbox(stubPinReader{configID: "cfg1"}, &stubTenantSandboxResolver{mgr: mgr}, nil)
	require.NoError(t, resetWorkspaceToEmpty(ctx, pinned, "s1", "sb1"))
	require.Equal(t, []string{"connect", "exec"}, client.ops,
		"SkipWorkspacePrep must omit the extra prepareSessionDirs exec")
	require.Contains(t, client.exec.Command, "commit-tree")
	require.Contains(t, client.exec.Command, "reset --hard")
	require.Contains(t, client.exec.Command, "clean -fdx")
	require.NotContains(t, client.exec.Command, `rm -rf "$GIT_DIR"`)
	require.NotContains(t, client.exec.Command, `find "$WORK_TREE"`)
	require.NotContains(t, client.exec.Command, `mkdir -p -- "$d"`)
}
