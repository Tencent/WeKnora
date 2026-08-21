package sandbox

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"iter"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

// --- fake Engine API ---------------------------------------------------------

// fakeDockerEngine is an in-memory Docker daemon: enough of the Engine API to
// drive the adapter's logic without a host that can run containers.
type fakeDockerEngine struct {
	pingErr error

	created     []client.ContainerCreateOptions
	createErr   error
	createdID   string
	started     []string
	startErr    error
	unpaused    []string
	removed     []string
	removeErr   error
	inspect     map[string]container.InspectResponse
	inspectErr  error
	list        []container.Summary
	listFilters []client.Filters
	listErr     error

	execOptions []client.ExecCreateOptions
	execStdout  string
	execStderr  string
	execExit    int
	execErr     error
	execStdin   bytes.Buffer

	statResult map[string]container.PathStat
	// statHook lets a test answer differently per call, which is how the
	// sweeper's re-check before deletion is exercised.
	statHook func(path string) (container.PathStat, bool)
	statErr  error
	copiedTo []client.CopyToContainerOptions
	copyFrom []byte
	copyErr  error

	images       []image.Summary
	imagePresent map[string]bool
	pulled       []string
}

func newFakeDockerEngine() *fakeDockerEngine {
	return &fakeDockerEngine{
		createdID:    "container-1",
		inspect:      make(map[string]container.InspectResponse),
		statResult:   make(map[string]container.PathStat),
		imagePresent: make(map[string]bool),
	}
}

func (f *fakeDockerEngine) Ping(context.Context, client.PingOptions) (client.PingResult, error) {
	return client.PingResult{APIVersion: "1.55"}, f.pingErr
}

func (f *fakeDockerEngine) ContainerCreate(
	_ context.Context, options client.ContainerCreateOptions,
) (client.ContainerCreateResult, error) {
	f.created = append(f.created, options)
	if f.createErr != nil {
		return client.ContainerCreateResult{}, f.createErr
	}
	return client.ContainerCreateResult{ID: f.createdID}, nil
}

func (f *fakeDockerEngine) ContainerStart(
	_ context.Context, id string, _ client.ContainerStartOptions,
) (client.ContainerStartResult, error) {
	f.started = append(f.started, id)
	return client.ContainerStartResult{}, f.startErr
}

func (f *fakeDockerEngine) ContainerUnpause(
	_ context.Context, id string, _ client.ContainerUnpauseOptions,
) (client.ContainerUnpauseResult, error) {
	f.unpaused = append(f.unpaused, id)
	return client.ContainerUnpauseResult{}, nil
}

func (f *fakeDockerEngine) ContainerInspect(
	_ context.Context, id string, _ client.ContainerInspectOptions,
) (client.ContainerInspectResult, error) {
	if f.inspectErr != nil {
		return client.ContainerInspectResult{}, f.inspectErr
	}
	found, ok := f.inspect[id]
	if !ok {
		return client.ContainerInspectResult{}, cerrdefs.ErrNotFound.WithMessage("no such container")
	}
	return client.ContainerInspectResult{Container: found}, nil
}

func (f *fakeDockerEngine) ContainerList(
	_ context.Context, options client.ContainerListOptions,
) (client.ContainerListResult, error) {
	f.listFilters = append(f.listFilters, options.Filters)
	if f.listErr != nil {
		return client.ContainerListResult{}, f.listErr
	}
	return client.ContainerListResult{Items: f.list}, nil
}

func (f *fakeDockerEngine) ContainerRemove(
	_ context.Context, id string, _ client.ContainerRemoveOptions,
) (client.ContainerRemoveResult, error) {
	f.removed = append(f.removed, id)
	return client.ContainerRemoveResult{}, f.removeErr
}

func (f *fakeDockerEngine) ExecCreate(
	_ context.Context, _ string, options client.ExecCreateOptions,
) (client.ExecCreateResult, error) {
	f.execOptions = append(f.execOptions, options)
	if f.execErr != nil {
		return client.ExecCreateResult{}, f.execErr
	}
	return client.ExecCreateResult{ID: "exec-1"}, nil
}

func (f *fakeDockerEngine) ExecAttach(
	_ context.Context, _ string, _ client.ExecAttachOptions,
) (client.ExecAttachResult, error) {
	var framed bytes.Buffer
	writeStdcopyFrame(&framed, 1, f.execStdout)
	writeStdcopyFrame(&framed, 2, f.execStderr)
	return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{
		Conn:   &fakeHijackedConn{stdin: &f.execStdin},
		Reader: bufio.NewReader(&framed),
	}}, nil
}

func (f *fakeDockerEngine) ExecInspect(
	_ context.Context, _ string, _ client.ExecInspectOptions,
) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{ExitCode: f.execExit}, nil
}

func (f *fakeDockerEngine) CopyToContainer(
	_ context.Context, _ string, options client.CopyToContainerOptions,
) (client.CopyToContainerResult, error) {
	if f.copyErr != nil {
		return client.CopyToContainerResult{}, f.copyErr
	}
	content, _ := io.ReadAll(options.Content)
	options.Content = bytes.NewReader(content)
	f.copiedTo = append(f.copiedTo, options)
	f.copyFrom = content
	return client.CopyToContainerResult{}, nil
}

func (f *fakeDockerEngine) CopyFromContainer(
	_ context.Context, _ string, _ client.CopyFromContainerOptions,
) (client.CopyFromContainerResult, error) {
	if f.copyErr != nil {
		return client.CopyFromContainerResult{}, f.copyErr
	}
	return client.CopyFromContainerResult{
		Content: io.NopCloser(bytes.NewReader(f.copyFrom)),
	}, nil
}

func (f *fakeDockerEngine) ContainerStatPath(
	_ context.Context, _ string, options client.ContainerStatPathOptions,
) (client.ContainerStatPathResult, error) {
	if f.statErr != nil {
		return client.ContainerStatPathResult{}, f.statErr
	}
	if f.statHook != nil {
		if stat, ok := f.statHook(options.Path); ok {
			return client.ContainerStatPathResult{Stat: stat}, nil
		}
	}
	stat, ok := f.statResult[options.Path]
	if !ok {
		return client.ContainerStatPathResult{}, cerrdefs.ErrNotFound.WithMessage("no such path")
	}
	return client.ContainerStatPathResult{Stat: stat}, nil
}

func (f *fakeDockerEngine) ImageInspect(
	_ context.Context, imageID string, _ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	if f.imagePresent[imageID] {
		return client.ImageInspectResult{}, nil
	}
	return client.ImageInspectResult{}, cerrdefs.ErrNotFound.WithMessage("no such image")
}

func (f *fakeDockerEngine) ImagePull(
	_ context.Context, ref string, _ client.ImagePullOptions,
) (client.ImagePullResponse, error) {
	f.pulled = append(f.pulled, ref)
	f.imagePresent[ref] = true
	return fakePullResponse{ReadCloser: io.NopCloser(strings.NewReader(`{"status":"Downloaded"}`))}, nil
}

func (f *fakeDockerEngine) ImageList(
	_ context.Context, _ client.ImageListOptions,
) (client.ImageListResult, error) {
	return client.ImageListResult{Items: f.images}, nil
}

// fakePullResponse satisfies the pull-response contract without a registry.
type fakePullResponse struct{ io.ReadCloser }

func (fakePullResponse) JSONMessages(context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(func(jsonstream.Message, error) bool) {}
}
func (fakePullResponse) Wait(context.Context) error { return nil }

// writeStdcopyFrame appends one multiplexed frame in the format the daemon
// uses for non-TTY exec streams.
func writeStdcopyFrame(buf *bytes.Buffer, stream byte, payload string) {
	if payload == "" {
		return
	}
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	buf.Write(header)
	buf.WriteString(payload)
}

// fakeHijackedConn stands in for the hijacked TCP connection. Only the write
// half matters: the adapter writes stdin and half-closes.
type fakeHijackedConn struct {
	stdin  *bytes.Buffer
	closed bool
}

func (c *fakeHijackedConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *fakeHijackedConn) Write(p []byte) (int, error)      { return c.stdin.Write(p) }
func (c *fakeHijackedConn) Close() error                     { c.closed = true; return nil }
func (c *fakeHijackedConn) CloseWrite() error                { return nil }
func (c *fakeHijackedConn) LocalAddr() net.Addr              { return nil }
func (c *fakeHijackedConn) RemoteAddr() net.Addr             { return nil }
func (c *fakeHijackedConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeHijackedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeHijackedConn) SetWriteDeadline(time.Time) error { return nil }

func newTestDockerClient(t *testing.T, engine *fakeDockerEngine) *DockerRemoteClient {
	t.Helper()
	settings, err := dockerSettingsFromConfig(&Config{
		Type:        SandboxTypeDocker,
		DockerImage: "weknora/sandbox:test",
	})
	require.NoError(t, err)
	// Idle sweeping is disabled: it would race the assertions with a
	// background goroutine deleting the very containers under test.
	settings.IdleTTL = 0
	return newDockerRemoteClientWithAPI(engine, settings)
}

func testHandle(id string) RemoteSandboxHandle {
	return &dockerSandboxHandle{id: id}
}

// --- tests -------------------------------------------------------------------

func TestDockerClientCreateAppliesIsolationAndMetadata(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.imagePresent["weknora/sandbox:test"] = true
	docker := newTestDockerClient(t, engine)

	handle, err := docker.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "weknora/sandbox:test",
		Metadata:   map[string]string{remoteMetadataSessionID: "sess-1"},
		EnvVars:    map[string]string{"FOO": "bar"},
		Timeout:    RemoteTimeoutPolicy{Mode: RemoteTimeoutExplicit, Value: 15 * time.Minute},
	})
	require.NoError(t, err)
	require.Equal(t, "container-1", handle.ID())
	require.Equal(t, SandboxTypeDocker, handle.Provider())
	require.Equal(t, "sess-1", handle.Metadata()[remoteMetadataSessionID])
	require.NotContains(t, handle.Metadata(), dockerManagedLabel,
		"the ownership marker is our bookkeeping, not caller metadata")

	require.Len(t, engine.created, 1)
	created := engine.created[0]
	// PID 1 both keeps the container alive and prepares the activity marker so
	// that root and the unprivileged sandbox user can each refresh it; the
	// idle sweeper reads nothing else.
	require.Equal(t, dockerSandboxPID1User, created.Config.User,
		"PID 1 must be root so the entrypoint can chmod the activity marker")
	require.Equal(t, "/bin/sh", created.Config.Entrypoint[0])
	require.Contains(t, created.Config.Entrypoint[2], "touch "+dockerActivityMarker)
	require.Contains(t, created.Config.Entrypoint[2], "chmod 666 "+dockerActivityMarker)
	require.Contains(t, created.Config.Entrypoint[2], "exec sleep infinity")
	require.NotNil(t, created.Config.Cmd,
		"an empty (not nil) Cmd is what resets the image's own CMD on the wire")
	require.Empty(t, created.Config.Cmd)
	require.Equal(t, SessionWorkspaceRoot, created.Config.WorkingDir)
	require.Equal(t, []string{"FOO=bar"}, created.Config.Env)
	require.Equal(t, "true", created.Config.Labels[dockerManagedLabel])
	require.Equal(t, "sess-1", created.Config.Labels[remoteMetadataSessionID])
	require.Equal(t, "900", created.Config.Labels[dockerIdleTTLLabel],
		"the sweep must reclaim with the TTL the sandbox was created with")

	host := created.HostConfig
	require.Equal(t, []string{"ALL"}, host.CapDrop)
	require.Equal(t, dockerSandboxCapabilities, host.CapAdd)
	require.Contains(t, host.SecurityOpt, "no-new-privileges")
	require.Equal(t, DefaultDockerMemoryLimit, host.Memory)
	require.Equal(t, host.Memory, host.MemorySwap, "swap must not soften the memory cap")
	require.Equal(t, int64(DefaultDockerCPULimit*1e9), host.NanoCPUs)
	require.Equal(t, DefaultDockerPidsLimit, *host.PidsLimit)
	require.Equal(t, container.NetworkMode("bridge"), host.NetworkMode)
	require.Equal(t, []string{"container-1"}, engine.started)
}

func TestDockerClientCreatePullsMissingImage(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	_, err := docker.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "weknora/sandbox:test",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"weknora/sandbox:test"}, engine.pulled)
}

// A container that cannot start is a leak waiting to happen: nothing binds it,
// so only the much later idle sweep would notice.
func TestDockerClientCreateRemovesContainerThatCannotStart(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.imagePresent["weknora/sandbox:test"] = true
	engine.startErr = errors.New("no space left on device")
	docker := newTestDockerClient(t, engine)

	_, err := docker.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "weknora/sandbox:test",
	})
	require.Error(t, err)
	require.Equal(t, []string{"container-1"}, engine.removed)
}

func TestDockerClientCreateRefusesVolumeMounts(t *testing.T) {
	docker := newTestDockerClient(t, newFakeDockerEngine())
	_, err := docker.Create(context.Background(), RemoteCreateRequest{
		TemplateID:   "weknora/sandbox:test",
		VolumeMounts: []RemoteVolumeMount{{Name: "skills", Path: "/skills"}},
	})
	require.Error(t, err)
	require.Equal(t, RemoteErrorKindUnsupported, remoteKind(err))
}

func TestDockerClientCreateNoEgressUsesNoneNetwork(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.imagePresent["weknora/sandbox:test"] = true
	docker := newTestDockerClient(t, engine)

	denied := false
	_, err := docker.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "weknora/sandbox:test",
		Network:    RemoteNetworkPolicy{AllowInternetAccess: &denied},
	})
	require.NoError(t, err)
	require.Equal(t, container.NetworkMode("none"), engine.created[0].HostConfig.NetworkMode)
}

// Connect is where a session survives a daemon restart: the container's
// filesystem is intact, so it is restarted rather than replaced.
func TestDockerClientConnectRestartsStoppedContainer(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.inspect["container-1"] = container.InspectResponse{
		ID:    "container-1",
		State: &container.State{Status: "exited"},
		Config: &container.Config{Labels: map[string]string{
			dockerManagedLabel:      "true",
			remoteMetadataSessionID: "sess-1",
		}},
	}
	docker := newTestDockerClient(t, engine)

	handle, err := docker.Connect(context.Background(), "container-1")
	require.NoError(t, err)
	require.Equal(t, "container-1", handle.ID())
	require.Equal(t, []string{"container-1"}, engine.started)
	require.Equal(t, "sess-1", handle.Metadata()[remoteMetadataSessionID])
}

func TestDockerClientConnectUnpausesPausedContainer(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.inspect["container-1"] = container.InspectResponse{
		ID:     "container-1",
		State:  &container.State{Status: "paused"},
		Config: &container.Config{},
	}
	docker := newTestDockerClient(t, engine)

	_, err := docker.Connect(context.Background(), "container-1")
	require.NoError(t, err)
	require.Equal(t, []string{"container-1"}, engine.unpaused)
	require.Empty(t, engine.started)
}

// A missing container must classify as NotFound so the lifecycle rebinds the
// session instead of failing every execution forever.
func TestDockerClientConnectMissingContainerIsReplaceable(t *testing.T) {
	docker := newTestDockerClient(t, newFakeDockerEngine())
	_, err := docker.Connect(context.Background(), "container-gone")
	require.Error(t, err)
	require.True(t, CanReplaceRemoteBinding(err))
}

func TestDockerClientGetNormalizesState(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.inspect["container-1"] = container.InspectResponse{
		ID: "container-1",
		State: &container.State{
			Status:    "running",
			StartedAt: "2026-08-12T10:00:00.000000000Z",
		},
		Config: &container.Config{
			Image:  "weknora/sandbox:test",
			Labels: map[string]string{remoteMetadataSessionID: "sess-1"},
		},
	}
	docker := newTestDockerClient(t, engine)

	summary, err := docker.Get(context.Background(), "container-1")
	require.NoError(t, err)
	require.Equal(t, RemoteStateRunning, summary.State)
	require.Equal(t, "running", summary.RawState)
	require.Equal(t, "weknora/sandbox:test", summary.TemplateID)
	require.Equal(t, 2026, summary.StartedAt.Year())
}

// "exited" must not be terminal: the filesystem is intact and Connect restarts
// it. Treating it as terminal would throw away a session's installed packages.
func TestDockerStateOfKeepsStoppedContainersResumable(t *testing.T) {
	require.Equal(t, RemoteStatePaused, dockerStateOf("exited"))
	require.Equal(t, RemoteStatePaused, dockerStateOf("paused"))
	require.Equal(t, RemoteStateRunning, dockerStateOf("running"))
	require.Equal(t, RemoteStateTerminal, dockerStateOf("dead"))
	require.Equal(t, RemoteStateTransitioning, dockerStateOf("restarting"))
}

func TestDockerClientListFiltersByOwnershipAndMetadata(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.list = []container.Summary{
		{
			ID: "a", State: "running", Image: "img", Created: 1700000000,
			Labels: map[string]string{dockerManagedLabel: "true", remoteMetadataSessionID: "s1"},
		},
		{
			ID: "b", State: "exited", Image: "img", Created: 1700000000,
			Labels: map[string]string{dockerManagedLabel: "true"},
		},
	}
	docker := newTestDockerClient(t, engine)

	all, err := docker.List(context.Background(), RemoteListFilter{
		Metadata: map[string]string{remoteMetadataSessionID: "s1"},
	})
	require.NoError(t, err)
	require.Len(t, all, 2, "the daemon does the metadata filtering; the fake does not")
	require.Len(t, engine.listFilters, 1)
	require.Contains(t, engine.listFilters[0]["label"], dockerManagedLabel+"=true")
	require.Contains(t, engine.listFilters[0]["label"], remoteMetadataSessionID+"=s1")

	running, err := docker.List(context.Background(), RemoteListFilter{
		States: []RemoteSandboxState{RemoteStateRunning},
	})
	require.NoError(t, err)
	require.Len(t, running, 1)
	require.Equal(t, "a", running[0].ID)
}

// The wrapper is the whole timeout story for this backend: cancelling the HTTP
// request does not stop the process, so the container must kill it.
func TestDockerClientExecWrapsCommandWithTimeoutAndActivityMarker(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "hello\n"
	docker := newTestDockerClient(t, engine)

	result, err := docker.Exec(context.Background(), testHandle("container-1"), RemoteExecRequest{
		Command: "python3",
		Args:    []string{"/workspace/script.py", "--flag"},
		Timeout: 45 * time.Second,
		User:    DefaultSandboxExecUser,
		WorkDir: SessionWorkspaceRoot,
		Env:     map[string]string{"K": "V"},
	})
	require.NoError(t, err)
	require.Equal(t, "hello\n", result.Stdout)

	require.Len(t, engine.execOptions, 1)
	opts := engine.execOptions[0]
	require.Equal(t, DefaultSandboxExecUser, opts.User)
	require.Equal(t, SessionWorkspaceRoot, opts.WorkingDir)
	require.Equal(t, []string{"K=V"}, opts.Env)
	require.Equal(t, "/bin/sh", opts.Cmd[0])
	require.Contains(t, opts.Cmd[2], dockerActivityMarker)
	require.Contains(t, opts.Cmd[2], "timeout -s KILL 45")
	require.Equal(t, []string{"weknora-exec", "python3", "/workspace/script.py", "--flag"},
		opts.Cmd[3:], "the command must reach the shell as positional args, never interpolated")
}

// A script containing shell metacharacters must not be re-interpreted by the
// wrapper that enforces the timeout.
func TestDockerClientExecShellPassesCommandAsPositionalArgument(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	_, err := docker.Exec(context.Background(), testHandle("container-1"), RemoteExecRequest{
		Command: `echo "a b"; rm -rf /nope`,
		Shell:   true,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	opts := engine.execOptions[0]
	require.Equal(t, []string{"weknora-exec", `echo "a b"; rm -rf /nope`}, opts.Cmd[3:])
	require.Equal(t, "root", opts.User, "an unspecified user means root, matching envd backends")
}

func TestDockerClientExecRejectsShellWithArgs(t *testing.T) {
	docker := newTestDockerClient(t, newFakeDockerEngine())
	_, err := docker.Exec(context.Background(), testHandle("c"), RemoteExecRequest{
		Command: "echo", Shell: true, Args: []string{"hi"},
	})
	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestDockerClientExecSeparatesStreamsAndReportsKill(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "out"
	engine.execStderr = "err"
	engine.execExit = 137
	docker := newTestDockerClient(t, engine)

	result, err := docker.Exec(context.Background(), testHandle("c"), RemoteExecRequest{
		Command: "sleep", Args: []string{"30"}, Timeout: time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, "out", result.Stdout)
	require.Equal(t, "err", result.Stderr)
	require.True(t, result.Killed, "SIGKILL from the timeout wrapper is a timeout, not a crash")
}

func TestDockerClientExecWritesStdin(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	_, err := docker.Exec(context.Background(), testHandle("c"), RemoteExecRequest{
		Command: "cat", Stdin: "payload\n", Timeout: time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, "payload\n", engine.execStdin.String())
	require.True(t, engine.execOptions[0].AttachStdin)
}

func TestDockerClientWriteFileUploadsArchiveOwnedBySandboxUser(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	err := docker.WriteFile(context.Background(), testHandle("c"),
		"/workspace/input/note.txt", []byte("hello"))
	require.NoError(t, err)

	require.Len(t, engine.copiedTo, 1)
	require.Equal(t, "/workspace/input", engine.copiedTo[0].DestinationPath)
	require.True(t, engine.copiedTo[0].CopyUIDGID)
	require.Contains(t, engine.execOptions[0].Cmd, "mkdir",
		"the parent directory has no archive-API equivalent and must be exec'd")
	require.Equal(t, DefaultSandboxExecUser, engine.execOptions[0].User,
		"mkdir as root would leave nested dirs unwritable by skill scripts")

	content, err := docker.ReadFile(context.Background(), testHandle("c"),
		"/workspace/input/note.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), content)
}

func TestDockerClientReadFileRejectsDirectoryArchive(t *testing.T) {
	engine := newFakeDockerEngine()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: "output", Typeflag: tar.TypeDir, Mode: 0o755,
	}))
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: "output/nested.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3,
	}))
	_, err := writer.Write([]byte("hi\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	engine.copyFrom = archive.Bytes()

	_, err = newTestDockerClient(t, engine).ReadFile(
		context.Background(), testHandle("c"), "/workspace/output")
	require.True(t, IsRemoteInvalidRequest(err),
		"a directory archive must not return the first nested file")
}

func TestDockerClientPathsMustBeAbsolute(t *testing.T) {
	docker := newTestDockerClient(t, newFakeDockerEngine())
	err := docker.WriteFile(context.Background(), testHandle("c"), "relative.txt", []byte("x"))
	require.True(t, IsRemoteInvalidRequest(err))

	_, statErr := docker.Stat(context.Background(), testHandle("c"), "")
	require.True(t, IsRemoteInvalidRequest(statErr))
}

func TestDockerClientRemoveRefusesContainerRoot(t *testing.T) {
	docker := newTestDockerClient(t, newFakeDockerEngine())
	err := docker.Remove(context.Background(), testHandle("c"), "/")
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestDockerClientListDirParsesFindOutput(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "d\t4096\t1786565482.1779913070\t/workspace/output/nested\n" +
		"f\t12\t1786565482.0000000000\t/workspace/output/report.txt\n"
	docker := newTestDockerClient(t, engine)

	entries, err := docker.ListDir(context.Background(), testHandle("c"), "/workspace/output")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, RemoteEntryDir, entries[0].Type)
	require.Equal(t, "nested", entries[0].Name)
	require.Equal(t, RemoteEntryFile, entries[1].Type)
	require.Equal(t, int64(12), entries[1].Size)
	require.Equal(t, 2026, entries[1].ModTime.Year())
}

func TestDockerClientListDirMissingDirectoryIsNotFound(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execExit = 1
	engine.execStderr = "find: '/workspace/nope': No such file or directory"
	docker := newTestDockerClient(t, engine)

	_, err := docker.ListDir(context.Background(), testHandle("c"), "/workspace/nope")
	require.True(t, IsRemoteNotFound(err))
}

func TestDockerClientStatMapsEntryType(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.statResult["/workspace/output"] = container.PathStat{
		Size: 4096, Mode: os.ModeDir | 0o755, Mtime: time.Now(),
	}
	docker := newTestDockerClient(t, engine)

	entry, err := docker.Stat(context.Background(), testHandle("c"), "/workspace/output")
	require.NoError(t, err)
	require.Equal(t, RemoteEntryDir, entry.Type)

	_, err = docker.Stat(context.Background(), testHandle("c"), "/workspace/missing")
	require.True(t, IsRemoteNotFound(err))
}

func TestDockerClientCapabilities(t *testing.T) {
	caps := newTestDockerClient(t, newFakeDockerEngine()).Capabilities()
	require.True(t, caps.SupportsReconnect)
	require.True(t, caps.SupportsMetadata)
	require.True(t, caps.SupportsListSandboxes)
	require.True(t, caps.SupportsFilesystemEnumeration)
	require.False(t, caps.SupportsTimeoutRefresh,
		"the daemon has no TTL to refresh; reclamation is WeKnora's own sweep")
	require.False(t, caps.SupportsVolumes)
}

func TestDockerErrorKindClassification(t *testing.T) {
	require.Equal(t, RemoteErrorKindNotFound,
		dockerErrorKind("Get", cerrdefs.ErrNotFound.WithMessage("nope")))
	require.Equal(t, RemoteErrorKindInvalidRequest,
		dockerErrorKind("Create", cerrdefs.ErrNotFound.WithMessage("no such image")),
		"a missing image is a bad template, not a vanished sandbox")
	require.Equal(t, RemoteErrorKindConflict,
		dockerErrorKind("Exec", cerrdefs.ErrConflict.WithMessage("not running")))
	require.Equal(t, RemoteErrorKindAuthentication,
		dockerErrorKind("List", cerrdefs.ErrPermissionDenied.WithMessage("denied")))
	require.Equal(t, RemoteErrorKindTimeout,
		dockerErrorKind("Exec", context.DeadlineExceeded))
	require.Equal(t, RemoteErrorKindInternal,
		dockerErrorKind("Exec", errors.New("boom")))
}

func TestValidateDockerHost(t *testing.T) {
	require.NoError(t, ValidateDockerHost("", false))
	require.NoError(t, ValidateDockerHost("unix:///var/run/docker.sock", false))
	require.Error(t, ValidateDockerHost("unix://relative.sock", false))
	require.Error(t, ValidateDockerHost("/var/run/docker.sock", false),
		"a bare path hides whether the endpoint is local or remote")
	require.Error(t, ValidateDockerHost("ssh://host", false))
	require.Error(t, ValidateDockerHost("tcp://10.0.0.5:2376", false),
		"a private daemon address needs the explicit private-endpoint opt-in")
	require.NoError(t, ValidateDockerHost("tcp://10.0.0.5:2376", true))
}

func TestValidateDockerRemoteTLS(t *testing.T) {
	require.NoError(t, ValidateDockerRemoteTLS("", ""))
	require.NoError(t, ValidateDockerRemoteTLS("unix:///var/run/docker.sock", ""))
	require.Error(t, ValidateDockerRemoteTLS("tcp://10.0.0.5:2376", ""),
		"a remote daemon without TLS is a plaintext root socket")
	require.NoError(t, ValidateDockerRemoteTLS("tcp://10.0.0.5:2376", "/etc/weknora/docker-certs"))
}

// The archive endpoints are served by the daemon and ignore the exec user, so
// they read and write as root. Pseudo-filesystems and the sweeper's own marker
// must not be addressable through them.
func TestDockerCleanPathRefusesReservedPaths(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)
	ctx := context.Background()

	for _, target := range []string{
		"/proc/1/environ",
		"/proc",
		"/sys/kernel",
		"/dev/mem",
		dockerActivityMarker,
		// path.Clean must run before the check, or traversal walks around it.
		"/workspace/../proc/1/environ",
	} {
		_, err := docker.ReadFile(ctx, testHandle("c"), target)
		require.Error(t, err, target)
		require.Equal(t, RemoteErrorKindInvalidRequest, remoteKind(err), target)

		err = docker.WriteFile(ctx, testHandle("c"), target, []byte("x"))
		require.Error(t, err, target)
	}

	require.Empty(t, engine.copiedTo, "a refused path must never reach the daemon")
}

func TestDockerHostNeedsDialGuard(t *testing.T) {
	require.True(t, dockerHostNeedsDialGuard("tcp://10.0.0.5:2376"))
	require.True(t, dockerHostNeedsDialGuard("https://daemon.example:2376"))
	require.False(t, dockerHostNeedsDialGuard("unix:///var/run/docker.sock"),
		"a unix socket carries no address the outbound policy could check")
	require.False(t, dockerHostNeedsDialGuard(""))
}

// Two configs pointing at the same daemon with different outbound policies must
// not share a pooled client: the client carries the dialer, so the stricter
// config would inherit connections it is not allowed to make.
func TestDockerEndpointKeySeparatesOutboundPolicy(t *testing.T) {
	permissive := dockerEndpoint{Host: "tcp://10.0.0.5:2376", AllowPrivate: true}
	restrictive := dockerEndpoint{Host: "tcp://10.0.0.5:2376"}
	require.NotEqual(t, permissive.key(), restrictive.key())
}

func TestDockerSettingsCarryOutboundPolicy(t *testing.T) {
	settings, err := dockerSettingsFromConfig(&Config{
		Type:                  SandboxTypeDocker,
		DockerImage:           "weknora/sandbox:test",
		AllowPrivateEndpoints: true,
	})
	require.NoError(t, err)
	require.True(t, settings.Endpoint.AllowPrivate)
}

func TestValidateDockerNetworkMode(t *testing.T) {
	require.NoError(t, ValidateDockerNetworkMode(""))
	require.NoError(t, ValidateDockerNetworkMode("bridge"))
	require.NoError(t, ValidateDockerNetworkMode("none"))
	require.Error(t, ValidateDockerNetworkMode("host"))
	require.Error(t, ValidateDockerNetworkMode("container:abc"))
	require.Error(t, ValidateDockerNetworkMode("ns:/var/run/netns/foo"))
	// A named network is usually the deployment's own compose network, which
	// would put the sandbox alongside Postgres and Redis.
	require.Error(t, ValidateDockerNetworkMode("weknora_default"))
	require.Error(t, ValidateDockerNetworkMode("weknora-sandbox"))
}

func TestDockerSettingsRejectHostNetworkAndPlaintextTCP(t *testing.T) {
	_, err := dockerSettingsFromConfig(&Config{
		Type:              SandboxTypeDocker,
		DockerImage:       "weknora/sandbox:test",
		DockerNetworkMode: "host",
	})
	require.Error(t, err)

	_, err = dockerSettingsFromConfig(&Config{
		Type:        SandboxTypeDocker,
		DockerImage: "weknora/sandbox:test",
		DockerHost:  "tcp://10.0.0.5:2376",
	})
	require.Error(t, err)
}

func TestDockerSettingsRequireImage(t *testing.T) {
	_, err := dockerSettingsFromConfig(&Config{Type: SandboxTypeDocker})
	require.Error(t, err)
}

func TestDockerSessionCreateRequestDeletesIdleSandboxes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeDocker
	cfg.DockerImage = "weknora/sandbox:test"
	applyDockerRuntimeDefaults(cfg)

	request, err := buildSessionCreateRequest(SandboxTypeDocker, cfg)
	require.NoError(t, err)
	require.Equal(t, "weknora/sandbox:test", request.TemplateID)
	require.Equal(t, DefaultDockerIdleTTL, request.Timeout.Value)
	require.Equal(t, RemoteOnTimeoutKill, request.Timeout.Action,
		"pausing a container keeps its memory on the host, so it reclaims nothing")
}
