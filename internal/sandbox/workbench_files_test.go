package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type workbenchTestClient struct {
	*fakeRemoteClient
	run         func(context.Context, RemoteSandboxHandle, RemoteExecRequest) (*RemoteExecResult, error)
	probeResult *RemoteExecResult
	probeErr    error
	probeCalls  int
}

func (c *workbenchTestClient) SupportsPrivateWorkbenchExec() bool { return true }

func (c *workbenchTestClient) Exec(
	ctx context.Context,
	handle RemoteSandboxHandle,
	req RemoteExecRequest,
) (*RemoteExecResult, error) {
	if req.Command == "python3" && len(req.Args) == 3 && req.Args[0] == "-I" &&
		req.Args[1] == "-c" && req.Args[2] == terminalRuntimeProbe {
		c.probeCalls++
		if c.probeResult != nil || c.probeErr != nil {
			return c.probeResult, c.probeErr
		}
		return &RemoteExecResult{
			Stdout: `{"contract":"weknora-workbench-runtime/v1","terminal":true,"files":true}`,
		}, nil
	}
	if _, err := c.fakeRemoteClient.Exec(ctx, handle, req); err != nil {
		return nil, err
	}
	return c.run(ctx, handle, req)
}

func (c *workbenchTestClient) OpenCommandTerminal(
	context.Context, RemoteSandboxHandle, CommandTerminalRequest,
) (CommandTerminal, error) {
	return nil, errors.New("test terminal was not expected to open")
}

func workbenchHarness(t *testing.T, bound bool) (*SessionBoundManager, *workbenchTestClient, context.Context) {
	t.Helper()
	client := &workbenchTestClient{fakeRemoteClient: newFakeRemoteClient(SandboxTypeDocker)}
	client.capabilities.SupportsCommandTerminals = true
	client.omitsInboundTokenCarrier = true
	client.run = func(context.Context, RemoteSandboxHandle, RemoteExecRequest) (*RemoteExecResult, error) {
		return &RemoteExecResult{Stdout: `{"ok":true,"result":{"path":"","entries":[]}}`}, nil
	}
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeDocker
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: NewMemorySessionSandboxBindingStore(),
		Checker: &fakeSessionExistenceChecker{exists: true}, SkipHealthProbe: true,
	})
	require.NoError(t, err)
	ctx := types.WithSandboxTenantID(context.Background(), 777)
	if bound {
		_, err := mgr.resolveSession(ctx, "workbench-test")
		require.NoError(t, err)
	}
	return mgr, client, ctx
}

func TestWorkbenchPathValidation(t *testing.T) {
	for _, p := range []string{
		"/etc/passwd", "..", "../a", "a/../b", ".", "a/./b", "a//b", "a/", `a\b`,
		`C:/a`, "a\x00b", "a\nb", "a\tb", "a\x7fb", "a\u0085b", string([]byte{0xff}),
		strings.Repeat("a", 256), strings.Repeat("a/", 128) + "b", ".weknora-workbench-reserved",
	} {
		t.Run(p, func(t *testing.T) {
			require.ErrorIs(t, validateWorkbenchPath(p, false), ErrWorkbenchPath)
			require.ErrorIs(
				t,
				validateWorkbenchRequest(WorkbenchFileRequest{Operation: "rename", Path: "a", NewPath: p}),
				ErrWorkbenchPath,
			)
		})
	}
	for _, p := range []string{
		"file", "a/b", "spaced name", "';$(touch x);.txt", "WEKNORA_STDIN_EOF", "\u4e2d\u6587.txt",
	} {
		require.NoError(t, validateWorkbenchPath(p, false))
	}
	require.NoError(t, validateWorkbenchRequest(WorkbenchFileRequest{Operation: "list"}))
	for _, op := range []string{"read", "write", "mkdir", "rename", "remove", "unknown", "_upload_begin"} {
		require.ErrorIs(t, validateWorkbenchRequest(WorkbenchFileRequest{Operation: op}), ErrWorkbenchPath)
	}
	require.ErrorIs(
		t,
		validateWorkbenchRequest(WorkbenchFileRequest{Operation: "read", Path: "a", NewPath: "b"}),
		ErrWorkbenchPath,
	)
	require.ErrorIs(
		t,
		validateWorkbenchRequest(WorkbenchFileRequest{Operation: "read", Path: "a", Content: []byte("x")}),
		ErrWorkbenchPath,
	)
	require.NoError(
		t,
		validateWorkbenchRequest(
			WorkbenchFileRequest{Operation: "write", Path: "a", Content: make([]byte, WorkbenchMaxFileBytes)},
		),
	)
	require.ErrorIs(
		t,
		validateWorkbenchRequest(
			WorkbenchFileRequest{Operation: "write", Path: "a", Content: make([]byte, WorkbenchMaxFileBytes+1)},
		),
		ErrWorkbenchTooLarge,
	)
}

type workbenchManagerWrapper struct {
	Manager
	SessionCapabilityProvider
	effective SandboxType
}

func (w workbenchManagerWrapper) GetType() SandboxType { return w.effective }

func TestWorkbenchCapabilityFailsClosed(t *testing.T) {
	mgr, _, ctx := workbenchHarness(t, false)
	provider, ok := WorkbenchFileProviderFrom(mgr)
	require.True(t, ok)
	require.Same(t, mgr, provider)
	wrapper := workbenchManagerWrapper{Manager: mgr, SessionCapabilityProvider: mgr, effective: SandboxTypeDocker}
	provider, ok = WorkbenchFileProviderFrom(wrapper)
	require.True(t, ok)
	require.Same(t, mgr, provider)
	require.False(
		t,
		workbenchPrivateExecSupported(&CubeRemoteClient{}),
		"Cube's command logger must not receive workbench bodies",
	)
	require.True(t, workbenchPrivateExecSupported(&E2BRemoteClient{}))
	var nilManager *SessionBoundManager
	for _, disabled := range []Manager{nil, nilManager, NewDisabledManager()} {
		provider, ok = WorkbenchFileProviderFrom(disabled)
		require.False(t, ok)
		require.Nil(t, provider)
	}
	mgr.mu.Lock()
	mgr.activeType = SandboxTypeDisabled
	mgr.mu.Unlock()
	_, err := mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	mgr.mu.Lock()
	mgr.activeType = SandboxTypeDocker
	mgr.mu.Unlock()
	require.NoError(t, mgr.Cleanup(ctx))
	provider, ok = WorkbenchFileProviderFrom(mgr)
	require.False(t, ok)
	require.Nil(t, provider)
	_, err = mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
}

func TestWorkbenchSessionLookupDoesNotProvision(t *testing.T) {
	mgr, client, ctx := workbenchHarness(t, false)
	_, err := mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	creates, connects, _, lists, _ := client.counts()
	require.Zero(t, creates)
	require.Zero(t, connects)
	require.Zero(t, lists)
	require.Empty(t, client.execRequests)
	_, err = mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "read", Path: "../outside"})
	require.ErrorIs(t, err, ErrWorkbenchPath)
	_, err = mgr.WorkbenchFiles(context.Background(), "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	_, err = mgr.WorkbenchFiles(ctx, "", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
}

func TestWorkbenchSessionOwnershipAndMissingRemote(t *testing.T) {
	mgr, client, ctx := workbenchHarness(t, true)
	_, err := mgr.WorkbenchFiles(
		types.WithSandboxTenantID(ctx, 778),
		"workbench-test",
		WorkbenchFileRequest{Operation: "list"},
	)
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	mgr.checker.(*fakeSessionExistenceChecker).setExists(false)
	_, err = mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrSandboxSessionDeleted)
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	require.Empty(t, client.execRequests)
	mgr.checker.(*fakeSessionExistenceChecker).setExists(true)
	require.NoError(t, client.Delete(ctx, "docker-1"))
	_, err = mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	creates, _, _, _, _ := client.counts()
	require.Equal(t, 1, creates)
}

func TestWorkbenchFixedExecProtocol(t *testing.T) {
	mgr, client, ctx := workbenchHarness(t, true)
	req := WorkbenchFileRequest{
		Operation: "write",
		Path:      "WEKNORA_STDIN_EOF';$(echo x).bin",
		Content:   []byte{0, 255, 13, 10},
	}
	client.run = func(ctx context.Context, _ RemoteSandboxHandle, remote RemoteExecRequest) (*RemoteExecResult, error) {
		require.Equal(t, "python3", remote.Command)
		require.Equal(t, []string{"-I", "-c", workbenchFileHelper}, remote.Args)
		require.False(t, remote.Shell)
		require.Equal(t, "/", remote.WorkDir)
		require.Equal(t, DefaultSandboxExecUser, remote.User)
		require.Equal(t, workbenchExecTimeout, remote.Timeout)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), workbenchExecTimeout)
		require.NotContains(t, remote.Stdin, "WEKNORA_STDIN_EOF")
		var decoded WorkbenchFileRequest
		require.NoError(t, json.Unmarshal([]byte(remote.Stdin), &decoded))
		require.Equal(t, req, decoded)
		result, err := json.Marshal(
			workbenchWireReply{OK: true, Result: &WorkbenchFileResult{Path: req.Path, Entries: []WorkbenchFileEntry{}}},
		)
		require.NoError(t, err)
		return &RemoteExecResult{Stdout: string(result)}, nil
	}
	result, err := mgr.WorkbenchFiles(ctx, "workbench-test", req)
	require.NoError(t, err)
	require.Equal(t, req.Path, result.Path)
	require.Len(t, client.execRequests, 1)
	require.Empty(t, client.makeDirPaths)
	require.Empty(t, client.writeFiles)
}

func TestWorkbenchProtocolErrors(t *testing.T) {
	cases := []struct {
		name string
		out  *RemoteExecResult
		err  error
		want error
	}{
		{"path", &RemoteExecResult{Stdout: `{"ok":false,"code":"path"}`}, nil, ErrWorkbenchPath},
		{"missing", &RemoteExecResult{Stdout: `{"ok":false,"code":"not_found"}`}, nil, ErrWorkbenchNotFound},
		{"conflict", &RemoteExecResult{Stdout: `{"ok":false,"code":"conflict"}`}, nil, ErrWorkbenchConflict},
		{"size", &RemoteExecResult{Stdout: `{"ok":false,"code":"too_large"}`}, nil, ErrWorkbenchTooLarge},
		{"unknown code", &RemoteExecResult{Stdout: `{"ok":false,"code":"future"}`}, nil, ErrWorkbenchUnavailable},
		{
			"stderr hidden",
			&RemoteExecResult{ExitCode: 1, Stderr: "sensitive remote data"},
			nil,
			ErrWorkbenchUnavailable,
		},
		{"killed", &RemoteExecResult{Killed: true}, nil, ErrWorkbenchUnavailable},
		{"nil", nil, nil, ErrWorkbenchUnavailable},
		{"transport", nil, errors.New("transport failed"), ErrWorkbenchUnavailable},
		{"bad JSON", &RemoteExecResult{Stdout: "not JSON"}, nil, ErrWorkbenchUnavailable},
		{"null", &RemoteExecResult{Stdout: "null"}, nil, ErrWorkbenchUnavailable},
		{"trailing JSON", &RemoteExecResult{Stdout: `{"ok":true} {}`}, nil, ErrWorkbenchUnavailable},
		{"unknown field", &RemoteExecResult{Stdout: `{"ok":true,"extra":"x"}`}, nil, ErrWorkbenchUnavailable},
		{"mixed status", &RemoteExecResult{Stdout: `{"ok":true,"code":"path"}`}, nil, ErrWorkbenchUnavailable},
		{"missing result", &RemoteExecResult{Stdout: `{"ok":true}`}, nil, ErrWorkbenchUnavailable},
		{
			"wrong path",
			&RemoteExecResult{Stdout: `{"ok":true,"result":{"path":"/etc","entries":[]}}`},
			nil,
			ErrWorkbenchUnavailable,
		},
		{"nil entries", &RemoteExecResult{Stdout: `{"ok":true,"result":{"path":""}}`}, nil, ErrWorkbenchUnavailable},
		{
			"unexpected content",
			&RemoteExecResult{Stdout: `{"ok":true,"result":{"path":"","entries":[],"content":"eA=="}}`},
			nil,
			ErrWorkbenchUnavailable,
		},
		{
			"bad base64",
			&RemoteExecResult{Stdout: `{"ok":true,"result":{"path":"","entries":[],"content":"?!"}}`},
			nil,
			ErrWorkbenchUnavailable,
		},
		{
			"oversized stdout",
			&RemoteExecResult{Stdout: strings.Repeat("x", workbenchMaxResponse+1)},
			nil,
			ErrWorkbenchTooLarge,
		},
		{"oversized stderr", &RemoteExecResult{Stderr: strings.Repeat("x", 4097)}, nil, ErrWorkbenchTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr, client, ctx := workbenchHarness(t, true)
			client.run = func(context.Context, RemoteSandboxHandle, RemoteExecRequest) (*RemoteExecResult, error) {
				return tc.out, tc.err
			}
			result, err := mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
			require.Nil(t, result)
			require.ErrorIs(t, err, tc.want)
			require.NotContains(t, err.Error(), "sensitive remote data")
		})
	}
}

func TestWorkbenchResultValidation(t *testing.T) {
	entry := WorkbenchFileEntry{Name: "a", Path: "a", Type: "file", Size: 1, ModifiedAt: time.Now()}
	for _, mutate := range []func(*WorkbenchFileEntry){
		func(e *WorkbenchFileEntry) { e.Name = "../a" }, func(e *WorkbenchFileEntry) { e.Path = "/a" },
		func(e *WorkbenchFileEntry) { e.Type = "symlink" }, func(e *WorkbenchFileEntry) { e.Size = -1 },
		func(e *WorkbenchFileEntry) { e.ModifiedAt = time.Time{} },
	} {
		bad := entry
		mutate(&bad)
		_, err := validateWorkbenchResult(WorkbenchFileRequest{Operation: "list"}, &workbenchWireReply{
			Result: &WorkbenchFileResult{Entries: []WorkbenchFileEntry{bad}},
		})
		require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	}
	for _, entries := range [][]WorkbenchFileEntry{{entry, entry}, make([]WorkbenchFileEntry, WorkbenchMaxEntries+1)} {
		_, err := validateWorkbenchResult(WorkbenchFileRequest{Operation: "list"}, &workbenchWireReply{
			Result: &WorkbenchFileResult{Entries: entries},
		})
		require.Error(t, err)
	}
	_, err := validateWorkbenchResult(WorkbenchFileRequest{Operation: "read", Path: "a"}, &workbenchWireReply{
		Result: &WorkbenchFileResult{
			Path:    "a",
			Entries: []WorkbenchFileEntry{},
			Content: make([]byte, WorkbenchMaxFileBytes+1),
		},
	})
	require.ErrorIs(t, err, ErrWorkbenchTooLarge)
}

func TestWorkbenchDeadlineAndLifecycleLock(t *testing.T) {
	mgr, client, ctx := workbenchHarness(t, true)
	entered, release := make(chan struct{}), make(chan struct{})
	client.run = func(ctx context.Context, _ RemoteSandboxHandle, _ RemoteExecRequest) (*RemoteExecResult, error) {
		close(entered)
		select {
		case <-release:
			return &RemoteExecResult{Stdout: `{"ok":true,"result":{"path":"","entries":[]}}`}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := mgr.WorkbenchFiles(callCtx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
		done <- err
	}()
	select {
	case <-entered:
	case <-callCtx.Done():
		t.Fatal("exec did not start")
	}
	destroyCtx, destroyCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer destroyCancel()
	require.ErrorIs(t, mgr.DestroySession(destroyCtx, "workbench-test"), context.DeadlineExceeded)
	close(release)
	require.NoError(t, <-done)
	client.run = func(ctx context.Context, _ RemoteSandboxHandle, _ RemoteExecRequest) (*RemoteExecResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	shortCtx, shortCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer shortCancel()
	_, err := mgr.WorkbenchFiles(shortCtx, "workbench-test", WorkbenchFileRequest{Operation: "list"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
}

func TestWorkbenchChunkedUploadProtocol(t *testing.T) {
	for _, failChunk := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cleanup on failure"}[failChunk], func(t *testing.T) {
			mgr, client, ctx := workbenchHarness(t, true)
			content := bytes.Repeat([]byte{0, 255, 1, 2}, WorkbenchMaxFileBytes/4)
			var uploaded []byte
			var operations []string
			client.run = func(
				_ context.Context, _ RemoteSandboxHandle, remote RemoteExecRequest,
			) (*RemoteExecResult, error) {
				require.LessOrEqual(t, len(remote.Stdin), workbenchMaxRequest)
				// Existing SDKs lower the complete request to one bash argument.
				require.Less(t, len(wrapWithStdin(buildShellLine(remote.Command, remote.Args), remote.Stdin)), 128<<10)
				var req workbenchWireRequest
				require.NoError(t, json.Unmarshal([]byte(remote.Stdin), &req))
				operations = append(operations, req.Operation)
				reply := workbenchWireReply{OK: true}
				switch req.Operation {
				case "_upload_begin":
					require.Regexp(t, `^\.weknora-workbench-[a-f0-9]{32}$`, req.Upload.Name)
					req.Upload.Device, req.Upload.Inode = 1, 99
					reply.Upload = req.Upload
				case "_upload_chunk":
					require.Equal(t, len(uploaded), req.Offset)
					require.LessOrEqual(t, len(req.Content), workbenchChunkBytes)
					if failChunk {
						return nil, errors.New("chunk failed")
					}
					uploaded = append(uploaded, req.Content...)
				case "write":
					require.Empty(t, req.Content)
					require.Equal(t, len(content), req.Size)
					digest := sha256.Sum256(uploaded)
					require.Equal(t, hex.EncodeToString(digest[:]), req.SHA256)
					reply.Result = &WorkbenchFileResult{Path: req.Path, Entries: []WorkbenchFileEntry{}}
				case "_upload_abort":
					require.Equal(t, uint64(99), req.Upload.Inode)
				default:
					t.Fatalf("unexpected operation %s", req.Operation)
				}
				payload, err := json.Marshal(reply)
				require.NoError(t, err)
				return &RemoteExecResult{Stdout: string(payload)}, nil
			}
			_, err := mgr.WorkbenchFiles(
				ctx,
				"workbench-test",
				WorkbenchFileRequest{Operation: "write", Path: "large", Content: content},
			)
			if failChunk {
				require.ErrorIs(t, err, ErrWorkbenchUnavailable)
				require.NotContains(t, operations, "write")
			} else {
				require.NoError(t, err)
				require.Equal(t, content, uploaded)
			}
			require.Equal(t, "_upload_abort", operations[len(operations)-1])
		})
	}
}

func TestWorkbenchPythonDescriptorSuite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor helper requires Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, python, "-I", filepath.Join(filepath.Dir(source), "workbench_files_test.py")).
		CombinedOutput()
	require.NoError(t, err, "%s", out)
	t.Logf("%s", out)
}
