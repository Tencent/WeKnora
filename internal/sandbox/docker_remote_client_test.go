package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"iter"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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
	// execStreamStalls hands back an output stream that never ends on its
	// own, which is what a long-running exec looks like to a client that
	// gives up on it. execStream is the stream handed to the last attach.
	execStreamStalls bool
	execStream       *stalledReader

	statResult map[string]container.PathStat
	// statHook lets a test answer differently per call, which is how the
	// sweeper's re-check before deletion is exercised.
	statHook func(path string) (container.PathStat, bool)
	statErr  error

	images       []image.Summary
	imagePresent map[string]bool
	pulled       []string

	committed      []client.ContainerCommitOptions
	commitID       string
	commitErr      error
	removedImages  []string
	removeImageErr error
}

func newFakeDockerEngine() *fakeDockerEngine {
	return &fakeDockerEngine{
		createdID:    "container-1",
		inspect:      make(map[string]container.InspectResponse),
		statResult:   make(map[string]container.PathStat),
		imagePresent: make(map[string]bool),
	}
}

var _ dockerEngineAPI = (*fakeDockerEngine)(nil)

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
	if f.execStreamStalls {
		release := make(chan struct{})
		f.execStream = &stalledReader{release: release}
		return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{
			Conn:   &fakeHijackedConn{stdin: &f.execStdin, release: release},
			Reader: bufio.NewReader(f.execStream),
		}}, nil
	}
	var framed bytes.Buffer
	writeStdcopyFrame(&framed, 1, f.execStdout)
	writeStdcopyFrame(&framed, 2, f.execStderr)
	return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{
		Conn:   &fakeHijackedConn{stdin: &f.execStdin},
		Reader: bufio.NewReader(&framed),
	}}, nil
}

// stalledReader blocks until the hijacked connection it is paired with is
// closed, then flushes the output a daemon still has buffered when a client
// hangs up. Both halves matter: the block keeps the copy goroutine alive past
// cancellation, and the flush is what that goroutine writes into the caller's
// output buffers afterwards.
type stalledReader struct {
	release <-chan struct{}
	once    sync.Once
	tail    io.Reader

	// drained reports that the copier consumed the stream to its end, which
	// is the only point at which the output buffers stop being written to.
	drained atomic.Bool
}

func (r *stalledReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		<-r.release
		var framed bytes.Buffer
		for i := 0; i < 4096; i++ {
			writeStdcopyFrame(&framed, 1, "flushed after hangup\n")
		}
		r.tail = &framed
	})
	n, err := r.tail.Read(p)
	if errors.Is(err, io.EOF) {
		r.drained.Store(true)
	}
	return n, err
}

func (f *fakeDockerEngine) ExecInspect(
	_ context.Context, _ string, _ client.ExecInspectOptions,
) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{ExitCode: f.execExit}, nil
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

func (f *fakeDockerEngine) ImageRemove(
	_ context.Context, imageID string, _ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	f.removedImages = append(f.removedImages, imageID)
	if f.removeImageErr != nil {
		return client.ImageRemoveResult{}, f.removeImageErr
	}
	canonical := dockerCanonicalSnapshotID(imageID)
	found := f.imagePresent[imageID] || f.imagePresent[canonical]
	kept := make([]image.Summary, 0, len(f.images))
	for _, item := range f.images {
		match := item.ID == imageID || item.ID == canonical
		for _, tag := range item.RepoTags {
			if tag == imageID || dockerCanonicalSnapshotID(tag) == canonical {
				match = true
				break
			}
		}
		if match {
			found = true
			continue
		}
		kept = append(kept, item)
	}
	if !found {
		return client.ImageRemoveResult{}, cerrdefs.ErrNotFound.WithMessage("no such image")
	}
	f.images = kept
	delete(f.imagePresent, imageID)
	delete(f.imagePresent, canonical)
	return client.ImageRemoveResult{}, nil
}

func (f *fakeDockerEngine) ContainerCommit(
	_ context.Context, _ string, options client.ContainerCommitOptions,
) (client.ContainerCommitResult, error) {
	f.committed = append(f.committed, options)
	if f.commitErr != nil {
		return client.ContainerCommitResult{}, f.commitErr
	}
	id := f.commitID
	if id == "" {
		id = "sha256:snapshot-1"
	}
	labels := map[string]string{}
	for _, change := range options.Changes {
		change = strings.TrimSpace(change)
		if !strings.HasPrefix(strings.ToUpper(change), "LABEL ") {
			continue
		}
		rest := strings.TrimSpace(change[len("LABEL "):])
		key, value, ok := strings.Cut(rest, "=")
		if !ok {
			continue
		}
		labels[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	ref := strings.TrimSpace(options.Reference)
	var tags []string
	if ref != "" {
		tags = []string{ref + ":latest"}
		f.imagePresent[ref] = true
		f.imagePresent[ref+":latest"] = true
	}
	f.imagePresent[id] = true
	f.images = append(f.images, image.Summary{ID: id, RepoTags: tags, Labels: labels})
	return client.ContainerCommitResult{ID: id}, nil
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
	stdin *bytes.Buffer
	// release unblocks the paired stalledReader, mirroring how closing the
	// real hijacked connection ends the output stream.
	release   chan struct{}
	closeOnce sync.Once
}

func (c *fakeHijackedConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *fakeHijackedConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }
func (c *fakeHijackedConn) Close() error {
	if c.release != nil {
		c.closeOnce.Do(func() { close(c.release) })
	}
	return nil
}
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
	require.NotNil(t, host.Init)
	require.True(t, *host.Init,
		"`sleep` never reaps, so without tini a long session fills PidsLimit with zombies")
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

// Every exec the daemon runs passes through dockerExecUser, so a caller that
// forgets to name an account has to lose privileges here rather than gain them.
// Falling back to root used to be a container-escape primitive: the artifact
// bootstrap chowns a path inside the session's own workspace, and chown follows
// symlinks, so root + a planted link meant the session could take ownership of
// /etc and rewrite passwd to give itself uid 0.
func TestDockerExecUserNeverFallsBackToRoot(t *testing.T) {
	require.Equal(t, DefaultSandboxExecUser, dockerExecUser(DefaultSandboxExecUser))
	require.Equal(t, "1000:1000", dockerExecUser("1000:1000"))
	require.Equal(t, DefaultSandboxExecUser, dockerExecUser(""))
	require.Equal(t, DefaultSandboxExecUser, dockerExecUser("   "))
}

// Cancelling an exec leaves the copy goroutine writing into the output buffers.
// Returning what they hold at that moment is a data race, so the adapter has to
// close the stream and wait for the copier before reading them. Fails under
// -race if the wait is dropped.
func TestDockerClientExecCancelWaitsForOutputCopier(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStreamStalls = true
	docker := newTestDockerClient(t, engine)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := docker.Exec(ctx, testHandle("container-1"), RemoteExecRequest{
			Command: "sleep 600",
			Shell:   true,
			User:    DefaultSandboxExecUser,
			Timeout: 10 * time.Minute,
		})
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err, "a cancelled exec must not report success")
	case <-time.After(dockerExecDrainGrace + 5*time.Second):
		t.Fatal("Exec did not return after its context was cancelled")
	}
	require.True(t, engine.execStream.drained.Load(),
		"Exec returned while the copy goroutine was still writing into the output buffers it reads from")
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
	require.Equal(t, DefaultSandboxExecUser, opts.User,
		"an unnamed account must resolve to the sandbox user, never to root")
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

// The archive endpoint would apply this write as root and resolve symlinks on
// the way, so a link planted under the writable workspace could redirect an
// upload onto a file the sandbox account cannot touch. Writing through exec
// puts the kernel back in charge.
func TestDockerClientWriteFileRunsAsSandboxUserOverExec(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	err := docker.WriteFile(context.Background(), testHandle("c"),
		"/workspace/input/note.txt", []byte("hello"))
	require.NoError(t, err)

	require.Len(t, engine.execOptions, 2, "one mkdir for the parent, one write")
	mkdir, write := engine.execOptions[0], engine.execOptions[1]

	require.Contains(t, mkdir.Cmd, "mkdir")
	require.Equal(t, DefaultSandboxExecUser, mkdir.User,
		"mkdir as root would leave nested dirs unwritable by skill scripts")

	require.Equal(t, DefaultSandboxExecUser, write.User)
	require.True(t, write.AttachStdin)
	require.Equal(t, "hello", engine.execStdin.String())
	require.Equal(t,
		[]string{"weknora-exec", "sh", "-c", `cat > "$1"`, "weknora-write", "/workspace/input/note.txt"},
		write.Cmd[3:],
		"the destination must reach the shell as a positional arg, never interpolated")
}

func TestDockerClientReadFileRunsAsSandboxUserOverExec(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "report body\n"
	docker := newTestDockerClient(t, engine)

	content, err := docker.ReadFile(context.Background(), testHandle("c"),
		"/workspace/output/report.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("report body\n"), content)

	require.Len(t, engine.execOptions, 1)
	require.Equal(t, DefaultSandboxExecUser, engine.execOptions[0].User)
	require.Equal(t,
		[]string{"weknora-exec", "cat", "--", "/workspace/output/report.txt"},
		engine.execOptions[0].Cmd[3:])
}

// A path the sandbox account cannot read must surface as a refusal rather than
// as content, and must stay distinguishable from a path that simply is not
// there: callers treat NotFound as "nothing produced yet".
func TestDockerClientReadFileMapsFailures(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		refused func(error) bool
	}{
		{
			name:    "missing path",
			stderr:  "cat: /workspace/output/gone.txt: No such file or directory",
			refused: IsRemoteNotFound,
		},
		{
			name:    "unreadable through a planted symlink",
			stderr:  "cat: /workspace/output/esc/secret.txt: Permission denied",
			refused: IsRemoteInvalidRequest,
		},
		{
			name:    "directory",
			stderr:  "cat: /workspace/output: Is a directory",
			refused: IsRemoteInvalidRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := newFakeDockerEngine()
			engine.execExit = 1
			engine.execStderr = tt.stderr
			docker := newTestDockerClient(t, engine)

			_, err := docker.ReadFile(context.Background(), testHandle("c"),
				"/workspace/output/probe")
			require.True(t, tt.refused(err), "got %v", err)
		})
	}
}

// The attack this closes: the sandbox account can write to /workspace, and
// every caller-facing guard is a string prefix test, so `ln -s /root
// /workspace/output/esc` used to leave the daemon reading /root as root.
// find does not follow links, so the link reports as itself and callers that
// require a regular file refuse it before any read is attempted.
func TestDockerClientStatReportsSymlinkAsOther(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "l\t4\t1786565482.0000000000\t/workspace/output/esc\n"
	docker := newTestDockerClient(t, engine)

	entry, err := docker.Stat(context.Background(), testHandle("c"),
		"/workspace/output/esc")
	require.NoError(t, err)
	require.Equal(t, RemoteEntryOther, entry.Type,
		"a symlink must not be reported as the file it points at")
}

// Guards the property the symlink fix rests on. The archive endpoints ignored
// the requested user and ran as root; if any file operation goes back to one,
// this catches it without needing a daemon to prove the consequence.
func TestDockerClientFileOperationsNeverRunAsRoot(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.execStdout = "f\t3\t1786565482.0000000000\t/workspace/output/a.txt\n"
	docker := newTestDockerClient(t, engine)
	ctx := context.Background()
	handle := testHandle("c")

	require.NoError(t, docker.WriteFile(ctx, handle, "/workspace/output/a.txt", []byte("x")))
	_, err := docker.ReadFile(ctx, handle, "/workspace/output/a.txt")
	require.NoError(t, err)
	_, err = docker.Stat(ctx, handle, "/workspace/output/a.txt")
	require.NoError(t, err)
	_, err = docker.ListDir(ctx, handle, "/workspace/output")
	require.NoError(t, err)
	require.NoError(t, docker.MakeDir(ctx, handle, "/workspace/output/sub"))
	require.NoError(t, docker.Remove(ctx, handle, "/workspace/output/a.txt"))

	require.NotEmpty(t, engine.execOptions)
	for i, opts := range engine.execOptions {
		require.Equal(t, DefaultSandboxExecUser, opts.User,
			"exec %d (%v) must not run as root", i, opts.Cmd)
	}
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
	engine.execStdout = "d\t4096\t1786565482.0000000000\t/workspace/output\n"
	docker := newTestDockerClient(t, engine)

	entry, err := docker.Stat(context.Background(), testHandle("c"), "/workspace/output")
	require.NoError(t, err)
	require.Equal(t, RemoteEntryDir, entry.Type)
	require.Equal(t, int64(4096), entry.Size)
	require.Equal(t,
		[]string{"weknora-exec", "find", "/workspace/output", "-maxdepth", "0", "-printf", `%y\t%s\t%T@\t%p\n`},
		engine.execOptions[0].Cmd[3:])

	missing := newFakeDockerEngine()
	missing.execExit = 1
	missing.execStderr = "find: '/workspace/missing': No such file or directory"
	_, err = newTestDockerClient(t, missing).Stat(
		context.Background(), testHandle("c"), "/workspace/missing")
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
	require.True(t, caps.SupportsSnapshots,
		"docker commit is the skill-image snapshot; without this flag install is refused")
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

// File operations run as the sandbox account, so the kernel decides what is
// reachable. This list covers what that account legitimately can touch but
// never should through this API: the container's own runtime state, and the
// sweeper's marker, which a session must not be able to backdate.
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

	require.Empty(t, engine.execOptions, "a refused path must never reach the daemon")
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
