package sandbox

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"

	"github.com/stretchr/testify/require"
)

// newTestCubeRemoteClient wires a real CubeRemoteClient at the cubeMockServer.
// Tests exercise the adapter through its public RemoteSandboxClient surface
// only — no intermediate backend interfaces exist below CubeRemoteClient.
func newTestCubeRemoteClient(t *testing.T, mock *cubeMockServer) *CubeRemoteClient {
	t.Helper()
	client, err := NewCubeRemoteClient(testConfig(t, mock))
	require.NoError(t, err)
	return client
}

func TestCubeRemoteClientProviderAndCapabilities(t *testing.T) {
	client := newTestCubeRemoteClient(t, newCubeMockServer(t))

	require.Equal(t, SandboxTypeCube, client.Provider())
	require.Equal(t, RemoteSandboxCapabilities{
		SupportsReconnect:             true,
		SupportsMetadata:              true,
		SupportsListSandboxes:         true,
		SupportsPauseResume:           true,
		SupportsTimeoutRefresh:        true,
		SupportsFilesystemEnumeration: true,
		SupportsSnapshots:             true,
	}, client.Capabilities())
}

func TestCubeRemoteClientCreateSnapshot(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)

	ref, err := client.CreateSnapshot(ctx, handle.ID(), "weknora-sk-cfg1-g1")

	require.NoError(t, err)
	require.Equal(t, "snap-1", ref.ID)
	require.Equal(t, []string{"weknora-sk-cfg1-g1"}, ref.Names)
	mock.mu.Lock()
	body := mock.snapshotCreateBody
	mock.mu.Unlock()
	require.Equal(t, "weknora-sk-cfg1-g1", body["name"])
}

func TestCubeRemoteClientCreateSnapshotRejectsEmptySandboxID(t *testing.T) {
	client := newTestCubeRemoteClient(t, newCubeMockServer(t))

	_, err := client.CreateSnapshot(context.Background(), "  ", "n")

	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestCubeRemoteClientDeleteSnapshotTreatsMissingAsSuccess(t *testing.T) {
	client := newTestCubeRemoteClient(t, newCubeMockServer(t))

	err := client.DeleteSnapshot(context.Background(), "snap-missing")

	require.NoError(t, err, "a missing snapshot must not fail the delete path")
}

func TestCubeRemoteClientDeleteSnapshotRejectsEmptySnapshotID(t *testing.T) {
	client := newTestCubeRemoteClient(t, newCubeMockServer(t))

	err := client.DeleteSnapshot(context.Background(), "  ")

	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestCubeRemoteClientDeleteSnapshotReturnsUnexpectedErrors(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.snapshotDeleteFailWith = http.StatusInternalServerError
	client := newTestCubeRemoteClient(t, mock)

	err := client.DeleteSnapshot(context.Background(), "snap-any")

	require.Error(t, err)
	require.False(t, IsRemoteNotFound(err))
}

func TestCubeRemoteClientListSnapshotsRejectsStuckPagination(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.snapshotStuckPagination = true
	client := newTestCubeRemoteClient(t, mock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.ListSnapshots(ctx, "")

	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestCubeRemoteClientListSnapshotsPagesAllResults(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.snapshotPageSize = 1
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()
	first, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	second, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	firstRef, err := client.CreateSnapshot(ctx, first.ID(), "weknora-sk-cfg1-g1")
	require.NoError(t, err)
	secondRef, err := client.CreateSnapshot(ctx, first.ID(), "weknora-sk-cfg2-g1")
	require.NoError(t, err)
	_, err = client.CreateSnapshot(ctx, second.ID(), "other")
	require.NoError(t, err)

	list, err := client.ListSnapshots(ctx, first.ID())

	require.NoError(t, err)
	require.Equal(t, []RemoteSnapshotRef{firstRef, secondRef}, list)
}

func TestCubeRemoteClientCreateWritesLifecyclePayload(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)

	handle, err := client.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:       RemoteTimeoutExplicit,
			Value:      15 * time.Minute,
			Action:     RemoteOnTimeoutPause,
			AutoResume: true,
		},
		Metadata: map[string]string{"owner": "session-a"},
		EnvVars:  map[string]string{"LANG": "C.UTF-8"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, handle.ID())
	require.Equal(t, SandboxTypeCube, handle.Provider())
	require.Equal(t, map[string]string{"owner": "session-a"}, handle.Metadata())

	mock.mu.Lock()
	body := mock.createBody
	mock.mu.Unlock()
	require.Equal(t, "template-a", body["templateID"])
	require.Equal(t, float64(900), body["timeout"])
	require.Equal(t, map[string]any{"owner": "session-a"}, body["metadata"])
	require.Equal(t, map[string]any{"LANG": "C.UTF-8"}, body["envVars"])
	require.Equal(t, map[string]any{
		"onTimeout":  "pause",
		"autoResume": true,
	}, body["lifecycle"])
}

func TestCubeRemoteClientCreateForwardsNetworkPolicy(t *testing.T) {
	mock := newCubeMockServer(t)
	client, err := NewCubeRemoteClient(testConfig(t, mock))
	require.NoError(t, err)
	deny := false
	privateSandbox := false

	_, err = client.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "template-a",
		Network: RemoteNetworkPolicy{
			AllowInternetAccess: &deny,
			AllowPublicTraffic:  &privateSandbox,
			AllowOut:            []string{"*.example.com"},
			DenyOut:             []string{"0.0.0.0/0"},
		},
	})
	require.NoError(t, err)

	mock.mu.Lock()
	body := mock.createBody
	mock.mu.Unlock()
	require.Equal(t, false, body["allowInternetAccess"])
	networkPayload, ok := body["network"].(map[string]any)
	require.True(t, ok, "network payload missing: %#v", body["network"])
	require.Equal(t, false, networkPayload["allowPublicTraffic"])
	require.Equal(t, []any{"*.example.com"}, networkPayload["allowOut"])
	require.Equal(t, []any{"0.0.0.0/0"}, networkPayload["denyOut"])
}

func TestCubeRemoteClientCreatePreservesTimeoutModes(t *testing.T) {
	t.Run("server default omits timeout", func(t *testing.T) {
		mock := newCubeMockServer(t)
		client := newTestCubeRemoteClient(t, mock)
		_, err := client.Create(context.Background(), RemoteCreateRequest{
			TemplateID: "template-a",
			Timeout: RemoteTimeoutPolicy{
				Mode:   RemoteTimeoutServerDefault,
				Action: RemoteOnTimeoutKill,
			},
		})
		require.NoError(t, err)
		mock.mu.Lock()
		body := mock.createBody
		mock.mu.Unlock()
		_, hasTimeout := body["timeout"]
		require.False(t, hasTimeout)
	})

	t.Run("negative means never", func(t *testing.T) {
		mock := newCubeMockServer(t)
		client := newTestCubeRemoteClient(t, mock)
		_, err := client.Create(context.Background(), RemoteCreateRequest{
			TemplateID: "template-a",
			Timeout: RemoteTimeoutPolicy{
				Mode:   RemoteTimeoutExplicit,
				Value:  -time.Hour,
				Action: RemoteOnTimeoutKill,
			},
		})
		require.NoError(t, err)
		mock.mu.Lock()
		body := mock.createBody
		mock.mu.Unlock()
		// Cube's three-value semantics send -1 verbatim as "never timeout".
		require.Equal(t, float64(-1), body["timeout"])
	})

	t.Run("auto resume requires pause", func(t *testing.T) {
		client := newTestCubeRemoteClient(t, newCubeMockServer(t))
		_, err := client.Create(context.Background(), RemoteCreateRequest{
			TemplateID: "template-a",
			Timeout: RemoteTimeoutPolicy{
				Mode:       RemoteTimeoutExplicit,
				Value:      time.Minute,
				Action:     RemoteOnTimeoutKill,
				AutoResume: true,
			},
		})
		require.True(t, IsRemoteInvalidRequest(err))
	})

	t.Run("missing template rejected before wire", func(t *testing.T) {
		mock := newCubeMockServer(t)
		client := newTestCubeRemoteClient(t, mock)
		_, err := client.Create(context.Background(), RemoteCreateRequest{})
		require.True(t, IsRemoteInvalidRequest(err))
		require.Zero(t, mock.createCount.Load())
	})
}

func TestCubeRemoteClientLifecycleRoundTrip(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  time.Minute,
			Action: RemoteOnTimeoutKill,
		},
	})
	require.NoError(t, err)

	summary, err := client.Get(ctx, handle.ID())
	require.NoError(t, err)
	require.Equal(t, handle.ID(), summary.ID)
	require.Equal(t, RemoteStateRunning, summary.State)

	list, err := client.List(ctx, RemoteListFilter{
		States: []RemoteSandboxState{RemoteStateRunning},
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, handle.ID(), list[0].ID)

	reconnected, err := client.Connect(ctx, handle.ID())
	require.NoError(t, err)
	require.Equal(t, handle.ID(), reconnected.ID())

	require.NoError(t, client.Delete(ctx, handle.ID()))
	require.Equal(t, int32(1), mock.killCount.Load())
	mock.mu.Lock()
	_, stillAlive := mock.sandboxes[handle.ID()]
	mock.mu.Unlock()
	require.False(t, stillAlive)
}

func TestCubeRemoteClientExecArgvAndShell(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()

	var (
		gotCmd  string
		gotArgs []string
	)
	mock.SetExecutor(func(_, cmd string, args []string) (string, string, int) {
		gotCmd = cmd
		gotArgs = args
		return "ok\n", "", 0
	})

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutServerDefault,
			Action: RemoteOnTimeoutKill,
		},
	})
	require.NoError(t, err)

	// Argv → cubeClient assembles /bin/bash -l -c "<shell-quoted argv>". The
	// mock records the wrapper argv, which lets us assert both the wrapper
	// shape and that the caller's arguments are shell-quoted (not lost).
	result, err := client.Exec(ctx, handle, RemoteExecRequest{
		Command: "python3",
		Args:    []string{"script.py", "argument with spaces"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Stdout, "ok")
	require.Equal(t, "/bin/bash", gotCmd)
	require.Len(t, gotArgs, 3)
	require.Equal(t, "-l", gotArgs[0])
	require.Equal(t, "-c", gotArgs[1])
	require.Contains(t, gotArgs[2], "python3")
	require.Contains(t, gotArgs[2], "'argument with spaces'")

	// Shell → the caller's raw expression is passed through verbatim.
	gotCmd, gotArgs = "", nil
	mock.SetExecutor(func(_, cmd string, args []string) (string, string, int) {
		gotCmd = cmd
		gotArgs = args
		return "shell\n", "", 0
	})
	_, err = client.Exec(ctx, handle, RemoteExecRequest{
		Command: "printf '%s' ok | cat",
		Shell:   true,
	})
	require.NoError(t, err)
	require.Equal(t, "/bin/bash", gotCmd)
	require.Equal(t, "printf '%s' ok | cat", gotArgs[2])

	// Shell + argv is mutually exclusive.
	_, err = client.Exec(ctx, handle, RemoteExecRequest{
		Command: "echo",
		Args:    []string{"unsafe ambiguity"},
		Shell:   true,
	})
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestCubeRemoteClientExecTimeoutIsKilled(t *testing.T) {
	mock := newCubeMockServer(t)
	// A slow executor lets the outer request timeout fire before the mock
	// returns a stream. cubeClient's RunCommand cancellation path then
	// synthesises Killed=true, ExitCode=-1.
	mock.SetExecutor(func(string, string, []string) (string, string, int) {
		time.Sleep(200 * time.Millisecond)
		return "", "", 0
	})
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
	})
	require.NoError(t, err)

	result, err := client.Exec(ctx, handle, RemoteExecRequest{
		Command: "sleep",
		Args:    []string{"10"},
		Timeout: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Killed)
	require.Equal(t, -1, result.ExitCode)
}

func TestCubeRemoteClientFileWriteRoundTrip(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()

	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)

	require.NoError(t, client.WriteFile(ctx, handle, "/workspace/hello.txt", []byte("hi")))
	require.NoError(t, client.MakeDir(ctx, handle, "/workspace/nested"))

	mock.mu.Lock()
	files := mock.files[handle.ID()]
	mock.mu.Unlock()
	require.Equal(t, "hi", string(files["/workspace/hello.txt"]))
}

func TestCubeRemoteClientRejectsForeignHandle(t *testing.T) {
	client := newTestCubeRemoteClient(t, newCubeMockServer(t))
	_, err := client.ReadFile(
		context.Background(),
		&contractHandle{id: "e2b-1", provider: SandboxTypeE2B},
		"/workspace/file",
	)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestCubeRemoteClientListFilters(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newTestCubeRemoteClient(t, mock)
	ctx := context.Background()

	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "keep"},
	})
	require.NoError(t, err)
	_, err = client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "other"},
	})
	require.NoError(t, err)

	list, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{"owner": "keep"},
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, handle.ID(), list[0].ID)
}

func TestNormalizeCubeState(t *testing.T) {
	tests := map[string]RemoteSandboxState{
		"running":    RemoteStateRunning,
		"paused":     RemoteStatePaused,
		"pausing":    RemoteStateTransitioning,
		"resuming":   RemoteStateTransitioning,
		"pending":    RemoteStateTransitioning,
		"killing":    RemoteStateTerminal,
		"killed":     RemoteStateTerminal,
		"terminated": RemoteStateTerminal,
		"deleted":    RemoteStateTerminal,
		"failed":     RemoteStateTerminal,
		"":           RemoteStateUnknown,
		"weird":      RemoteStateUnknown,
	}
	for raw, want := range tests {
		require.Equalf(t, want, normalizeCubeState(raw), "state %q", raw)
	}
}

func TestNormalizeCubeError(t *testing.T) {
	tests := []struct {
		name string
		op   string
		err  error
		kind RemoteErrorKind
	}{
		{"sandbox not found", "Get", cubesandbox.ErrSandboxNotFound, RemoteErrorKindNotFound},
		{"template not found", "Create", cubesandbox.ErrTemplateNotFound, RemoteErrorKindInvalidRequest},
		{"authentication", "Health", cubesandbox.ErrAuthentication, RemoteErrorKindAuthentication},
		{"path not found", "Stat", &cubesandbox.NotFoundError{Path: "/x"}, RemoteErrorKindNotFound},
		{"gone", "Get", &cubesandbox.APIError{StatusCode: http.StatusGone}, RemoteErrorKindTerminal},
		{"conflict", "Connect", &cubesandbox.APIError{StatusCode: http.StatusConflict}, RemoteErrorKindConflict},
		{"rate limited", "Create", &cubesandbox.APIError{StatusCode: http.StatusTooManyRequests}, RemoteErrorKindCapacity},
		{"bad gateway", "List", &cubesandbox.APIError{StatusCode: http.StatusBadGateway}, RemoteErrorKindUnavailable},
		{"deadline", "Exec", context.DeadlineExceeded, RemoteErrorKindTimeout},
		{"unknown", "List", errors.New("unknown"), RemoteErrorKindInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := normalizeCubeError(tt.op, tt.err)
			var remoteErr *RemoteError
			require.ErrorAs(t, err, &remoteErr)
			require.Equal(t, tt.kind, remoteErr.Kind)
			require.Equal(t, SandboxTypeCube, remoteErr.Provider)
			require.ErrorIs(t, err, tt.err)
		})
	}
}
