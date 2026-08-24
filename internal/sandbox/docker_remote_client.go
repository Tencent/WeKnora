// Docker backend as a session-persistent RemoteSandboxClient.
//
// One sandbox is one long-lived container: PID 1 sleeps, and every script,
// shell command and file operation runs against that container until the
// session ends or the idle sweep reclaims it. That is what makes the docker
// backend behave like the E2B one — same session state, same shell_exec, same
// attachment staging — instead of the previous one-shot `docker run --rm`,
// which could not keep anything between two executions.
//
// The mapping onto the Engine API:
//
//	Create   → POST /containers/create + /start, metadata as labels
//	Connect  → GET  /containers/{id}/json, restarting a stopped container
//	Get/List → GET  /containers/json?filters=label=…
//	Delete   → DELETE /containers/{id}?force=1
//	Exec     → POST /containers/{id}/exec → /exec/{id}/start (hijack)
//	Files    → PUT/GET/HEAD /containers/{id}/archive
//
// Three operations have no Engine API and are implemented as exec:
// MakeDir, Remove and ListDir. ListDir uses `find -printf`, which needs GNU
// findutils in the image; the standard WeKnora sandbox image provides it.
//
// Two Docker facts shape the rest of this file:
//
//   - Cancelling an exec client-side does NOT stop the process inside the
//     container. Every exec is therefore wrapped in the container's own
//     timeout(1), which is the only thing that actually kills a runaway script.
//   - The daemon has no idle timeout. Each exec refreshes an activity marker
//     file (for free, inside the same wrapper) that dockerIdleSweeper reads to
//     decide what to reclaim.

package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// dockerActivityMarker is touched by every exec and read by the idle sweeper.
// It lives outside /workspace so a script cannot mistake it for its own data,
// and outside /tmp so a tmpfs mount cannot hide it.
const dockerActivityMarker = "/var/lib/weknora-sandbox-activity"

// dockerSandboxEntrypoint keeps the container alive without running anything —
// the container is a place to exec into, not a service — and prepares the
// activity marker on the way.
//
// The marker has to be writable by every account that can exec, because
// scripts run as the unprivileged sandbox user while shell commands and
// filesystem helpers run as root. Creating it here, in the container's own
// entrypoint, is the only way to get that without spending an extra API round
// trip per sandbox: whoever PID 1 runs as owns the file, and the chmod that
// follows lets the other account refresh it. Without this the idle sweeper
// would see a session that only ever ran scripts as untouched, and reclaim it
// out from under the user.
var dockerSandboxEntrypoint = []string{
	"/bin/sh", "-c",
	"touch " + dockerActivityMarker + " 2>/dev/null; " +
		"chmod 666 " + dockerActivityMarker + " 2>/dev/null; " +
		"exec sleep infinity",
}

// DockerRemoteClient implements RemoteSandboxClient on top of one Docker
// daemon. It is safe for concurrent use: the moby client is, and this type
// holds no mutable state.
type DockerRemoteClient struct {
	api      dockerEngineAPI
	settings dockerRuntimeSettings

	// sweeper reclaims idle containers. Nil disables idle reclamation, which
	// is only appropriate for the connectivity-check client.
	sweeper *dockerIdleSweeper
}

// dockerRuntimeSettings is the per-config slice of Config the adapter reads.
type dockerRuntimeSettings struct {
	Image       string
	CPULimit    float64
	MemoryBytes int64
	PidsLimit   int64
	NetworkMode string
	Runtime     string
	IdleTTL     time.Duration
	HTTPTimeout time.Duration
	Endpoint    dockerEndpoint
}

// NewDockerRemoteClient builds the adapter for one workspace config, reusing
// the shared connection to that daemon.
func NewDockerRemoteClient(cfg *Config) (*DockerRemoteClient, error) {
	settings, err := dockerSettingsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	api, err := sharedDockerEngineClients.get(settings.Endpoint)
	if err != nil {
		return nil, err
	}
	return newDockerRemoteClientWithAPI(withDockerRPCTimeout(api, settings.HTTPTimeout), settings), nil
}

// NewDockerRemoteClientForCheck builds a client for the connectivity check.
//
// It differs from the resolved client in one way: no idle sweeping. A probe
// runs against a config an admin is still editing, and a half-finished config
// must never delete containers a working config owns.
func NewDockerRemoteClientForCheck(cfg *Config) (*DockerRemoteClient, error) {
	settings, err := dockerSettingsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	api, err := sharedDockerEngineClients.get(settings.Endpoint)
	if err != nil {
		return nil, err
	}
	settings.IdleTTL = 0
	return newDockerRemoteClientWithAPI(withDockerRPCTimeout(api, settings.HTTPTimeout), settings), nil
}

// newDockerRemoteClientWithAPI is the seam unit tests use: it takes any
// dockerEngineAPI, including an in-memory fake.
func newDockerRemoteClientWithAPI(
	api dockerEngineAPI,
	settings dockerRuntimeSettings,
) *DockerRemoteClient {
	adapter := &DockerRemoteClient{api: api, settings: settings}
	if settings.IdleTTL > 0 {
		adapter.sweeper = newDockerIdleSweeper(adapter, settings.IdleTTL)
	}
	return adapter
}

// dockerSettingsFromConfig projects Config, applying the built-in defaults for
// every value the workspace config leaves unset.
func dockerSettingsFromConfig(cfg *Config) (dockerRuntimeSettings, error) {
	if cfg == nil {
		return dockerRuntimeSettings{}, errors.New("sandbox: docker client requires a config")
	}
	image := strings.TrimSpace(cfg.DockerImage)
	if image == "" {
		return dockerRuntimeSettings{}, errors.New("sandbox: docker backend requires an image")
	}
	settings := dockerRuntimeSettings{
		Image:       image,
		CPULimit:    cfg.DockerCPULimit,
		MemoryBytes: cfg.DockerMemoryBytes,
		PidsLimit:   cfg.DockerPidsLimit,
		NetworkMode: strings.TrimSpace(cfg.DockerNetworkMode),
		Runtime:     strings.TrimSpace(cfg.DockerRuntime),
		IdleTTL:     cfg.DockerIdleTTL,
		HTTPTimeout: cfg.DockerHTTPTimeout,
		Endpoint: dockerEndpoint{
			Host:         strings.TrimSpace(cfg.DockerHost),
			TLSCertPath:  strings.TrimSpace(cfg.DockerTLSCertPath),
			AllowPrivate: cfg.AllowPrivateEndpoints,
			Timeout:      cfg.DockerHTTPTimeout,
		},
	}
	if settings.CPULimit <= 0 {
		settings.CPULimit = DefaultDockerCPULimit
	}
	if settings.MemoryBytes <= 0 {
		settings.MemoryBytes = DefaultDockerMemoryLimit
	}
	if settings.PidsLimit <= 0 {
		settings.PidsLimit = DefaultDockerPidsLimit
	}
	if settings.IdleTTL <= 0 {
		settings.IdleTTL = DefaultDockerIdleTTL
	}
	if settings.HTTPTimeout <= 0 {
		settings.HTTPTimeout = DefaultDockerHTTPTimeout
		settings.Endpoint.Timeout = DefaultDockerHTTPTimeout
	}
	if err := ValidateDockerNetworkMode(settings.NetworkMode); err != nil {
		return dockerRuntimeSettings{}, err
	}
	if err := ValidateDockerRemoteTLS(settings.Endpoint.Host, settings.Endpoint.TLSCertPath); err != nil {
		return dockerRuntimeSettings{}, err
	}
	return settings, nil
}

// dockerSandboxHandle is the opaque reference the manager holds.
type dockerSandboxHandle struct {
	id       string
	metadata map[string]string
}

func (h *dockerSandboxHandle) ID() string                  { return h.id }
func (h *dockerSandboxHandle) Provider() RemoteProvider    { return SandboxTypeDocker }
func (h *dockerSandboxHandle) Metadata() map[string]string { return h.metadata }

// Provider identifies this backend.
func (c *DockerRemoteClient) Provider() RemoteProvider { return SandboxTypeDocker }

// Capabilities reports what this backend can do.
//
// SupportsTimeoutRefresh is false because the daemon has no timeout to
// refresh: idle reclamation is WeKnora's own sweep, not a provider feature.
// SupportsVolumes is false until the volume-mount surface is mapped onto
// Docker named volumes; advertising it early would let a workspace configure
// a mount that silently never appears.
func (c *DockerRemoteClient) Capabilities() RemoteSandboxCapabilities {
	return RemoteSandboxCapabilities{
		SupportsReconnect:             true,
		SupportsMetadata:              true,
		SupportsListSandboxes:         true,
		SupportsPauseResume:           true,
		SupportsTimeoutRefresh:        false,
		SupportsFilesystemEnumeration: true,
		SupportsVolumes:               false,
	}
}

// Health pings the daemon.
func (c *DockerRemoteClient) Health(ctx context.Context) error {
	if _, err := c.api.Ping(ctx, client.PingOptions{}); err != nil {
		return dockerError("Health", err)
	}
	return nil
}

// Create starts a new container for one sandbox.
func (c *DockerRemoteClient) Create(
	ctx context.Context,
	req RemoteCreateRequest,
) (RemoteSandboxHandle, error) {
	if len(req.VolumeMounts) > 0 {
		return nil, &RemoteError{
			Kind:     RemoteErrorKindUnsupported,
			Provider: SandboxTypeDocker,
			Op:       "Create",
			Message:  "docker backend does not mount volumes yet",
		}
	}
	image := strings.TrimSpace(req.TemplateID)
	if image == "" {
		image = c.settings.Image
	}
	if err := c.ensureImage(ctx, image); err != nil {
		return nil, err
	}

	labels := dockerContainerLabels(req.Metadata)
	labels[dockerIdleTTLLabel] = strconv.Itoa(int(c.effectiveIdleTTL(req.Timeout).Seconds()))
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Image: image,
		Config: &container.Config{
			// The standard image ends with USER user. The entrypoint has to
			// create and chmod the activity marker under /var/lib, which only
			// root can do; exec still names DefaultSandboxExecUser per call.
			User: dockerSandboxPID1User,
			// Entrypoint rather than Cmd, with Cmd explicitly emptied: the
			// daemon prepends the image's own ENTRYPOINT to Cmd, so an image
			// that declares one (the cube target of docker/Dockerfile.sandbox
			// does, and so do most custom images) would take over PID 1. The
			// activity marker would then never exist and every exec would run
			// against whatever that entrypoint started instead.
			Entrypoint: dockerSandboxEntrypoint,
			Cmd:        []string{},
			WorkingDir: SessionWorkspaceRoot,
			Labels:     labels,
			Env:        dockerEnvSlice(req.EnvVars),
		},
		HostConfig: c.hostConfig(req.Network),
	})
	if err != nil {
		return nil, dockerError("Create", err)
	}
	if _, err := c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		// A container that cannot start is useless and would otherwise linger
		// as an unbound leftover the sweep only reclaims much later.
		c.removeQuietly(ctx, created.ID)
		return nil, dockerError("Create", err)
	}
	c.sweepInBackground(ctx)
	return &dockerSandboxHandle{id: created.ID, metadata: dockerSandboxMetadata(labels)}, nil
}

// effectiveIdleTTL resolves how long this sandbox may sit unused.
//
// The caller's timeout policy is honoured because it is what the session
// layer already expresses per provider; the configured value is the fallback.
// The action (pause vs kill) is not: Docker's pause keeps the container's
// memory resident on the host, so pausing an abandoned sandbox would reclaim
// nothing. Idle containers are always deleted, which matches what the
// lifecycle does with a sandbox its provider reaped.
func (c *DockerRemoteClient) effectiveIdleTTL(policy RemoteTimeoutPolicy) time.Duration {
	if policy.Mode == RemoteTimeoutExplicit && policy.Value > 0 {
		return policy.Value
	}
	return c.settings.IdleTTL
}

// hostConfig builds the isolation and resource envelope for a new container.
func (c *DockerRemoteClient) hostConfig(policy RemoteNetworkPolicy) *container.HostConfig {
	pids := c.settings.PidsLimit
	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Memory: c.settings.MemoryBytes,
			// Equal memory and memory+swap disables swap, so a runaway
			// allocation is killed instead of thrashing the host's disk.
			MemorySwap: c.settings.MemoryBytes,
			NanoCPUs:   int64(c.settings.CPULimit * 1e9),
			PidsLimit:  &pids,
		},
		CapDrop:     []string{"ALL"},
		CapAdd:      dockerSandboxCapabilities,
		SecurityOpt: []string{"no-new-privileges"},
		Runtime:     c.settings.Runtime,
		NetworkMode: container.NetworkMode(c.networkMode(policy)),
	}
	return hostConfig
}

// networkMode resolves the effective Docker network for a sandbox.
//
// Docker filters at L3/L4 only, so the domain-level allow/deny lists in
// RemoteNetworkPolicy cannot be honoured here; the one thing that maps
// cleanly is "no egress at all", which AllowInternetAccess=false expresses.
// Domain rules are silently not applied — the config surface refuses them
// before they get this far (see RequireCompleteConfig).
func (c *DockerRemoteClient) networkMode(policy RemoteNetworkPolicy) string {
	if policy.AllowInternetAccess != nil && !*policy.AllowInternetAccess {
		return "none"
	}
	if c.settings.NetworkMode != "" {
		return c.settings.NetworkMode
	}
	return "bridge"
}

// Connect re-attaches to an existing container, resuming it when the daemon
// or the host stopped it. This is the docker equivalent of E2B's auto-resume:
// a stopped container keeps its filesystem, so the session continues where it
// left off instead of losing everything it installed.
func (c *DockerRemoteClient) Connect(
	ctx context.Context,
	sandboxID string,
) (RemoteSandboxHandle, error) {
	inspected, err := c.api.ContainerInspect(ctx, sandboxID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, dockerError("Connect", err)
	}
	state := inspected.Container.State
	if state == nil {
		return nil, dockerError("Connect", errors.New("daemon returned no container state"))
	}
	switch dockerStateOf(state.Status) {
	case RemoteStateTerminal:
		return nil, &RemoteError{
			Kind:     RemoteErrorKindTerminal,
			Provider: SandboxTypeDocker,
			Op:       "Connect",
			Message:  "container is dead",
		}
	case RemoteStatePaused:
		if err := c.resume(ctx, inspected.Container.ID, string(state.Status)); err != nil {
			return nil, err
		}
	}
	c.sweepInBackground(ctx)

	var labels map[string]string
	if inspected.Container.Config != nil {
		labels = inspected.Container.Config.Labels
	}
	return &dockerSandboxHandle{
		id:       inspected.Container.ID,
		metadata: dockerSandboxMetadata(labels),
	}, nil
}

// resume brings a paused or stopped container back to running.
func (c *DockerRemoteClient) resume(ctx context.Context, id, status string) error {
	if strings.EqualFold(status, "paused") {
		if _, err := c.api.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{}); err != nil {
			return dockerError("Connect", err)
		}
		return nil
	}
	if _, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return dockerError("Connect", err)
	}
	return nil
}

// Get returns one sandbox summary.
func (c *DockerRemoteClient) Get(
	ctx context.Context,
	sandboxID string,
) (*RemoteSandboxSummary, error) {
	inspected, err := c.api.ContainerInspect(ctx, sandboxID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, dockerError("Get", err)
	}
	summary := &RemoteSandboxSummary{
		ID:        inspected.Container.ID,
		StartedAt: dockerContainerStartedAt(inspected.Container.State),
	}
	if inspected.Container.Config != nil {
		summary.TemplateID = inspected.Container.Config.Image
		summary.Metadata = dockerSandboxMetadata(inspected.Container.Config.Labels)
	}
	if inspected.Container.State != nil {
		summary.RawState = string(inspected.Container.State.Status)
		summary.State = dockerStateOf(inspected.Container.State.Status)
		if finished, err := time.Parse(
			time.RFC3339Nano, inspected.Container.State.FinishedAt,
		); err == nil && finished.Year() > 1 {
			summary.EndAt = finished.UTC()
		}
	}
	return summary, nil
}

// List enumerates the containers this backend owns, filtered server-side by
// the metadata labels the caller asked for.
func (c *DockerRemoteClient) List(
	ctx context.Context,
	filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	filters := client.Filters{}.Add("label", dockerManagedLabel+"=true")
	for key, value := range filter.Metadata {
		filters = filters.Add("label", key+"="+value)
	}
	listed, err := c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		return nil, dockerError("List", err)
	}

	wanted := make(map[RemoteSandboxState]struct{}, len(filter.States))
	for _, state := range filter.States {
		wanted[state] = struct{}{}
	}
	summaries := make([]RemoteSandboxSummary, 0, len(listed.Items))
	for _, item := range listed.Items {
		state := dockerStateOf(item.State)
		if len(wanted) > 0 {
			if _, ok := wanted[state]; !ok {
				continue
			}
		}
		summaries = append(summaries, RemoteSandboxSummary{
			ID:         item.ID,
			TemplateID: item.Image,
			State:      state,
			RawState:   string(item.State),
			Metadata:   dockerSandboxMetadata(item.Labels),
			StartedAt:  time.Unix(item.Created, 0).UTC(),
		})
	}
	return summaries, nil
}

// Delete removes a container and its anonymous volumes.
func (c *DockerRemoteClient) Delete(ctx context.Context, sandboxID string) error {
	_, err := c.api.ContainerRemove(ctx, sandboxID, client.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	if err != nil {
		return dockerError("Delete", err)
	}
	return nil
}

// removeQuietly deletes a container on a cleanup path where the caller
// already has a more interesting error to report.
func (c *DockerRemoteClient) removeQuietly(ctx context.Context, id string) {
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), remoteCleanupTimeout,
	)
	defer cancel()
	_, _ = c.api.ContainerRemove(cleanupCtx, id, client.ContainerRemoveOptions{
		Force: true, RemoveVolumes: true,
	})
}

// Exec runs one command inside the sandbox.
//
// The command is wrapped so that the container, not the client, enforces the
// timeout: cancelling the HTTP request leaves the process running (verified in
// docs/poc/docker-sandbox), which would let a runaway script keep burning the
// host's CPU long after WeKnora reported a timeout to the user.
func (c *DockerRemoteClient) Exec(
	ctx context.Context,
	handle RemoteSandboxHandle,
	req RemoteExecRequest,
) (*RemoteExecResult, error) {
	id, err := dockerHandleID("Exec", handle)
	if err != nil {
		return nil, err
	}
	if req.Shell && len(req.Args) > 0 {
		return nil, dockerInvalidRequest("Exec", "shell requests must not carry args")
	}
	if strings.TrimSpace(req.Command) == "" {
		return nil, dockerInvalidRequest("Exec", "command is required")
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	// The client deadline is deliberately looser than the in-container one so
	// the wrapper gets to report the kill itself, which is what turns a
	// timeout into Killed=true rather than a transport error.
	execCtx, cancel := context.WithTimeout(ctx, timeout+dockerExecGrace)
	defer cancel()

	created, err := c.api.ExecCreate(execCtx, id, client.ExecCreateOptions{
		Cmd:          dockerExecCommand(req, timeout),
		User:         dockerExecUser(req.User),
		WorkingDir:   req.WorkDir,
		Env:          dockerEnvSlice(req.Env),
		AttachStdin:  req.Stdin != "",
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, dockerError("Exec", err)
	}

	start := time.Now()
	stdout, stderr, err := c.streamExec(execCtx, created.ID, req.Stdin)
	if err != nil {
		return nil, err
	}
	inspected, err := c.api.ExecInspect(execCtx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, dockerError("Exec", err)
	}

	result := &RemoteExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: inspected.ExitCode,
		Duration: time.Since(start),
	}
	if dockerExecWasKilled(inspected.ExitCode) {
		result.Killed = true
	}
	return result, nil
}

// dockerExecGrace is the slack between the in-container timeout and the
// client-side deadline. It covers the round-trip and the wrapper's own
// teardown so a script killed at the deadline is still reported as Killed
// instead of surfacing as a transport timeout.
const dockerExecGrace = 10 * time.Second

// streamExec starts the exec, writes stdin, and demultiplexes the output.
func (c *DockerRemoteClient) streamExec(
	ctx context.Context,
	execID string,
	stdin string,
) (string, string, error) {
	attached, err := c.api.ExecAttach(ctx, execID, client.ExecAttachOptions{})
	if err != nil {
		return "", "", dockerError("Exec", err)
	}
	// Closing is what unblocks the copy goroutine below, so the cancellation
	// path needs to do it before the deferred close would.
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(attached.Close) }
	defer closeStream()

	if stdin != "" {
		if _, err := attached.Conn.Write([]byte(stdin)); err != nil {
			return "", "", dockerError("Exec", fmt.Errorf("write stdin: %w", err))
		}
	}
	// Always half-close: a command reading stdin (cat, python -) hangs
	// forever on an open write side, even when nothing was sent.
	if closer, ok := attached.Conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}

	// These buffers belong to the copy goroutine until it reports on done.
	// Reading them any earlier races with StdCopy writing into them, which is
	// why the cancellation path closes the stream and waits instead of
	// returning whatever happens to be there.
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
		done <- copyErr
	}()

	select {
	case copyErr := <-done:
		if copyErr != nil && !errors.Is(copyErr, io.EOF) {
			return stdout.String(), stderr.String(), dockerError("Exec", copyErr)
		}
		return stdout.String(), stderr.String(), nil
	case <-ctx.Done():
		closeStream()
		select {
		case <-done:
			return stdout.String(), stderr.String(), dockerError("Exec", ctx.Err())
		case <-time.After(dockerExecDrainGrace):
			// StdCopy did not return even with the stream closed, so it still
			// owns the buffers and the partial output has to be dropped.
			return "", "", dockerError("Exec", ctx.Err())
		}
	}
}

// dockerExecDrainGrace bounds how long a cancelled exec waits for its output
// copier to notice the closed stream.
const dockerExecDrainGrace = 5 * time.Second

// dockerExecCommand builds the argv actually handed to the daemon.
//
// The wrapper does two things no Engine API call can: it refreshes the
// activity marker the idle sweep reads, and it enforces the timeout inside
// the container. Positional arguments ("$@" / "$1") carry the caller's
// command through the shell without any quoting, so a script containing
// quotes or newlines cannot change what runs.
func dockerExecCommand(req RemoteExecRequest, timeout time.Duration) []string {
	seconds := strconv.Itoa(int(timeout.Round(time.Second).Seconds()))
	if seconds == "0" {
		seconds = "1"
	}
	touch := "touch " + dockerActivityMarker + " 2>/dev/null || true; "
	if req.Shell {
		return []string{
			"/bin/sh", "-c",
			touch + `exec timeout -s KILL ` + seconds + ` /bin/sh -c "$1"`,
			"weknora-exec", req.Command,
		}
	}
	argv := []string{
		"/bin/sh", "-c",
		touch + `exec timeout -s KILL ` + seconds + ` "$@"`,
		"weknora-exec", req.Command,
	}
	return append(argv, req.Args...)
}

// dockerExecUser resolves which account a command runs as.
//
// An empty user is reserved for the manager's own bootstrap, which chowns the
// artifact directory TO the sandbox account and therefore cannot run as it. It
// is not a general default: the envd-backed backends resolve a blank user to
// DefaultSandboxExecUser (E2B authenticates the data plane as that account,
// Cube hands the field to envd, which defaults the same way), so a path that
// left this to the adapter would run as root here and unprivileged there.
// Every caller-reachable path names the account explicitly.
func dockerExecUser(user string) string {
	if strings.TrimSpace(user) == "" {
		return "root"
	}
	return user
}

// dockerExecWasKilled reports whether an exit code means the wrapper killed
// the process. 137 is SIGKILL (timeout -s KILL), 124 is timeout(1) reporting
// that it had to intervene.
func dockerExecWasKilled(exitCode int) bool {
	return exitCode == 137 || exitCode == 124
}

// WriteFile uploads one file through the archive endpoint.
func (c *DockerRemoteClient) WriteFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	filePath string,
	content []byte,
) error {
	id, err := dockerHandleID("WriteFile", handle)
	if err != nil {
		return err
	}
	clean, err := dockerCleanPath("WriteFile", filePath)
	if err != nil {
		return err
	}
	if err := c.makeDir(ctx, id, path.Dir(clean), "WriteFile"); err != nil {
		return err
	}

	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	header := &tar.Header{
		Name:    path.Base(clean),
		Mode:    0o644,
		Size:    int64(len(content)),
		ModTime: time.Now(),
		// Files land owned by the sandbox account so scripts can read (and
		// replace) their own inputs; the archive endpoint applies these ids
		// verbatim when CopyUIDGID is set.
		Uid: dockerSandboxUID,
		Gid: dockerSandboxGID,
	}
	if err := writer.WriteHeader(header); err != nil {
		return dockerError("WriteFile", err)
	}
	if _, err := writer.Write(content); err != nil {
		return dockerError("WriteFile", err)
	}
	if err := writer.Close(); err != nil {
		return dockerError("WriteFile", err)
	}

	_, err = c.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{
		DestinationPath: path.Dir(clean),
		Content:         &archive,
		CopyUIDGID:      true,
	})
	if err != nil {
		return dockerError("WriteFile", err)
	}
	return nil
}

// dockerSandboxUID / dockerSandboxGID are the ids of DefaultSandboxExecUser.
// The template contract fixes them at 1000, the same convention E2B templates
// use, so uploaded files can be owned correctly without an extra lookup.
const (
	dockerSandboxUID = 1000
	dockerSandboxGID = 1000

	// dockerSandboxPID1User overrides the image USER so the entrypoint can
	// create the world-writable activity marker. Numeric 0 does not depend
	// on a root account existing by name in a custom image.
	dockerSandboxPID1User = "0"
)

// ReadFile downloads one file through the archive endpoint.
func (c *DockerRemoteClient) ReadFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	filePath string,
) ([]byte, error) {
	id, err := dockerHandleID("ReadFile", handle)
	if err != nil {
		return nil, err
	}
	clean, err := dockerCleanPath("ReadFile", filePath)
	if err != nil {
		return nil, err
	}
	response, err := c.api.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{
		SourcePath: clean,
	})
	if err != nil {
		return nil, dockerError("ReadFile", err)
	}
	defer func() { _ = response.Content.Close() }()

	reader := tar.NewReader(response.Content)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, &RemoteError{
				Kind:     RemoteErrorKindNotFound,
				Provider: SandboxTypeDocker,
				Op:       "ReadFile",
				Message:  "archive contained no regular file for " + clean,
			}
		}
		if err != nil {
			return nil, dockerError("ReadFile", err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			return nil, dockerInvalidRequest("ReadFile", clean+" is a directory")
		case tar.TypeReg:
			content, err := io.ReadAll(reader)
			if err != nil {
				return nil, dockerError("ReadFile", err)
			}
			return content, nil
		default:
			continue
		}
	}
}

// Stat returns metadata for one path.
func (c *DockerRemoteClient) Stat(
	ctx context.Context,
	handle RemoteSandboxHandle,
	filePath string,
) (*RemoteStatEntry, error) {
	id, err := dockerHandleID("Stat", handle)
	if err != nil {
		return nil, err
	}
	clean, err := dockerCleanPath("Stat", filePath)
	if err != nil {
		return nil, err
	}
	stat, err := c.api.ContainerStatPath(ctx, id, client.ContainerStatPathOptions{Path: clean})
	if err != nil {
		return nil, dockerError("Stat", err)
	}
	entry := &RemoteStatEntry{
		Path:    clean,
		Type:    RemoteEntryFile,
		Size:    stat.Stat.Size,
		ModTime: stat.Stat.Mtime,
	}
	if stat.Stat.Mode.IsDir() {
		entry.Type = RemoteEntryDir
	} else if !stat.Stat.Mode.IsRegular() {
		entry.Type = RemoteEntryOther
	}
	return entry, nil
}

// MakeDir creates a directory (and its parents) inside the sandbox.
func (c *DockerRemoteClient) MakeDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	dirPath string,
) error {
	id, err := dockerHandleID("MakeDir", handle)
	if err != nil {
		return err
	}
	clean, err := dockerCleanPath("MakeDir", dirPath)
	if err != nil {
		return err
	}
	return c.makeDir(ctx, id, clean, "MakeDir")
}

func (c *DockerRemoteClient) makeDir(ctx context.Context, id, dir, op string) error {
	result, err := c.Exec(ctx, &dockerSandboxHandle{id: id}, RemoteExecRequest{
		Command: "mkdir",
		Args:    []string{"-p", dir},
		User:    DefaultSandboxExecUser,
		Timeout: dockerFilesystemOpTimeout,
	})
	if err != nil {
		return dockerError(op, err)
	}
	if result.ExitCode != 0 {
		return &RemoteError{
			Kind:     RemoteErrorKindInvalidRequest,
			Provider: SandboxTypeDocker,
			Op:       op,
			Message:  fmt.Sprintf("mkdir -p %s: %s", dir, firstNonEmptyLine(result.Stderr)),
		}
	}
	return nil
}

// Remove deletes a path recursively.
func (c *DockerRemoteClient) Remove(
	ctx context.Context,
	handle RemoteSandboxHandle,
	targetPath string,
) error {
	id, err := dockerHandleID("Remove", handle)
	if err != nil {
		return err
	}
	clean, err := dockerCleanPath("Remove", targetPath)
	if err != nil {
		return err
	}
	if clean == "/" {
		return dockerInvalidRequest("Remove", "refusing to remove the container root")
	}
	result, err := c.Exec(ctx, &dockerSandboxHandle{id: id}, RemoteExecRequest{
		Command: "rm",
		Args:    []string{"-rf", clean},
		User:    DefaultSandboxExecUser,
		Timeout: dockerFilesystemOpTimeout,
	})
	if err != nil {
		return dockerError("Remove", err)
	}
	if result.ExitCode != 0 {
		return &RemoteError{
			Kind:     RemoteErrorKindInvalidRequest,
			Provider: SandboxTypeDocker,
			Op:       "Remove",
			Message:  fmt.Sprintf("rm -rf %s: %s", clean, firstNonEmptyLine(result.Stderr)),
		}
	}
	return nil
}

// ListDir lists one directory level.
//
// find(1) is used rather than ls because a single -printf format yields the
// type, size, mtime and path the caller needs, with a tab separator that
// cannot appear in find's own output fields.
func (c *DockerRemoteClient) ListDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	dirPath string,
) ([]RemoteDirEntry, error) {
	id, err := dockerHandleID("ListDir", handle)
	if err != nil {
		return nil, err
	}
	clean, err := dockerCleanPath("ListDir", dirPath)
	if err != nil {
		return nil, err
	}
	result, err := c.Exec(ctx, &dockerSandboxHandle{id: id}, RemoteExecRequest{
		Command: "find",
		Args: []string{
			clean, "-mindepth", "1", "-maxdepth", "1",
			"-printf", `%y\t%s\t%T@\t%p\n`,
		},
		User:    DefaultSandboxExecUser,
		Timeout: dockerFilesystemOpTimeout,
	})
	if err != nil {
		return nil, dockerError("ListDir", err)
	}
	if result.ExitCode != 0 {
		if strings.Contains(result.Stderr, "No such file or directory") {
			return nil, &RemoteError{
				Kind:     RemoteErrorKindNotFound,
				Provider: SandboxTypeDocker,
				Op:       "ListDir",
				Message:  clean + " does not exist",
			}
		}
		return nil, &RemoteError{
			Kind:     RemoteErrorKindInternal,
			Provider: SandboxTypeDocker,
			Op:       "ListDir",
			Message:  fmt.Sprintf("find %s: %s", clean, firstNonEmptyLine(result.Stderr)),
		}
	}
	return parseDockerFindOutput(result.Stdout), nil
}

// dockerFilesystemOpTimeout bounds the exec-backed filesystem helpers. They
// are all single syscalls in practice; a longer budget would only delay the
// report of a wedged container.
const dockerFilesystemOpTimeout = 30 * time.Second

// parseDockerFindOutput turns `find -printf '%y\t%s\t%T@\t%p\n'` lines into
// directory entries, skipping anything malformed rather than failing the
// whole listing: one unreadable entry must not hide the rest of a directory.
func parseDockerFindOutput(output string) []RemoteDirEntry {
	var entries []RemoteDirEntry
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 4)
		if len(fields) != 4 {
			continue
		}
		entry := RemoteDirEntry{
			Path: fields[3],
			Name: path.Base(fields[3]),
			Type: dockerEntryType(fields[0]),
		}
		if size, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			entry.Size = size
		}
		if seconds, err := strconv.ParseFloat(fields[2], 64); err == nil {
			entry.ModTime = time.Unix(
				int64(seconds), int64((seconds-float64(int64(seconds)))*1e9),
			).UTC()
		}
		entries = append(entries, entry)
	}
	return entries
}

func dockerEntryType(findType string) RemoteDirEntryType {
	switch findType {
	case "f":
		return RemoteEntryFile
	case "d":
		return RemoteEntryDir
	default:
		return RemoteEntryOther
	}
}

// ensureImage pulls the template image when the daemon does not have it.
//
// The pull is bounded by dockerImagePullBudget, not by the caller's deadline:
// a cold pull of the sandbox image takes minutes, and the settings wizard
// starts that pull in the background so a later Create usually just inspects.
func (c *DockerRemoteClient) ensureImage(ctx context.Context, image string) error {
	if _, err := c.api.ImageInspect(ctx, image); err == nil {
		return nil
	}
	pullCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dockerImagePullBudget)
	defer cancel()
	body, err := c.api.ImagePull(pullCtx, image, client.ImagePullOptions{})
	if err != nil {
		return dockerError("Create", fmt.Errorf("pull image %s: %w", image, err))
	}
	if err := awaitImagePull(pullCtx, body); err != nil {
		return dockerError("Create", fmt.Errorf("pull image %s: %w", image, err))
	}
	return nil
}

// sweepInBackground triggers a rate-limited idle sweep without adding latency
// to the request that happened to trigger it.
func (c *DockerRemoteClient) sweepInBackground(ctx context.Context) {
	if c.sweeper == nil {
		return
	}
	c.sweeper.trigger(ctx)
}

// dockerHandleID validates a handle belongs to this backend.
func dockerHandleID(op string, handle RemoteSandboxHandle) (string, error) {
	if handle == nil {
		return "", dockerInvalidRequest(op, "sandbox handle is required")
	}
	if handle.Provider() != SandboxTypeDocker {
		return "", dockerInvalidRequest(op, "handle belongs to provider "+string(handle.Provider()))
	}
	id := strings.TrimSpace(handle.ID())
	if id == "" {
		return "", dockerInvalidRequest(op, "sandbox handle has no ID")
	}
	return id, nil
}

// dockerCleanPath normalizes an absolute in-sandbox path. Relative paths are
// refused: they would resolve against the container's working directory,
// which differs between exec-backed and archive-backed operations.
func dockerCleanPath(op, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", dockerInvalidRequest(op, "path is required")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", dockerInvalidRequest(op, "path must be absolute: "+raw)
	}
	clean := path.Clean(trimmed)
	if reserved := dockerReservedPath(clean); reserved != "" {
		return "", dockerInvalidRequest(op, "path is not addressable: "+reserved)
	}
	return clean, nil
}

// dockerReservedPathPrefixes are refused for every file operation.
//
// Unlike the envd-backed backends, the archive endpoints (WriteFile, ReadFile,
// Stat) are served by the daemon itself and therefore ignore the exec user
// entirely: they read and write as root no matter what a caller asks for. The
// callers in this repository only ever address /workspace, so this is defence
// in depth rather than a live hole — but it is what keeps a future caller from
// turning a file read into a way to lift PID 1's environment out of /proc, or
// a file write into a way to backdate the sweeper's own bookkeeping.
var dockerReservedPathPrefixes = []string{
	"/proc",
	"/sys",
	"/dev",
	dockerActivityMarker,
}

// dockerReservedPath returns the prefix clean is refused for, or "" when the
// path is addressable. clean must already be path.Clean'd.
func dockerReservedPath(clean string) string {
	for _, prefix := range dockerReservedPathPrefixes {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return prefix
		}
	}
	return ""
}

// dockerEnvSlice converts an env map into Docker's KEY=VALUE form.
func dockerEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(env))
	for key, value := range env {
		pairs = append(pairs, key+"="+value)
	}
	return pairs
}

// firstNonEmptyLine trims a command's stderr down to something a user-facing
// error can carry.
func firstNonEmptyLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len(trimmed) > 200 {
				return trimmed[:200] + "…"
			}
			return trimmed
		}
	}
	return ""
}

var _ RemoteSandboxClient = (*DockerRemoteClient)(nil)
