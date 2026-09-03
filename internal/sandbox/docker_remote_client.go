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
//	Snapshot → POST /commit (skill images under weknora-skill/)
//
// Every file operation — WriteFile, ReadFile, Stat, MakeDir, Remove, ListDir —
// is an exec running as the sandbox account, NOT a call to /archive. The
// archive endpoints run as root and resolve symlinks, so a session that plants
// a link inside its own workspace could read or overwrite anything in the
// container through them. Going through exec puts the kernel back in charge of
// who may touch what. Do not "simplify" these back onto /archive.
//
// ListDir and Stat use `find -printf`, which needs GNU findutils in the image;
// the standard WeKnora sandbox image provides it.
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
// The marker has to be writable by the sandbox account. PID 1 runs as root so
// that it can create the file at all, but every exec that would refresh it —
// scripts, shell commands, filesystem helpers — runs as the unprivileged user.
// Creating it here, in the container's own entrypoint, avoids an extra API
// round trip per sandbox, and the chmod that follows is what lets that account
// touch it. Without this the idle sweeper would see a session that only ever
// ran scripts as untouched, and reclaim it out from under the user.
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
		// docker commit produces a local image whose tag is a template ID,
		// which is what skill install uses on Cube/E2B. The snapshot is
		// filesystem-only (no memory) and lives on this daemon.
		SupportsSnapshots: true,
		SupportsVolumes:   false,
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
	// ContainerStart returning is not the same as State.Running: the daemon
	// accepts the start, then PID 1 still has to replace sh. ExecCreate on a
	// container that is still "created" comes back as 409 Conflict
	// ("container is not running"), which used to fail skill install on
	// the first attempt and succeed on retry.
	if err := c.waitUntilRunning(ctx, created.ID, "Create"); err != nil {
		c.removeQuietly(ctx, created.ID)
		return nil, err
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
		// The entrypoint is a plain `sleep`, which never calls wait(). Without
		// tini in front of it, a background process outliving the exec that
		// started it becomes an unreaped zombie on exit, and a long session
		// accumulates them until PidsLimit is exhausted and every further exec
		// fails.
		Init: &dockerInitProcess,
	}
	return hostConfig
}

// dockerInitProcess is addressable because HostConfig.Init is a *bool: unset
// means "follow the daemon default", which is not what this backend wants.
var dockerInitProcess = true

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
		if err := c.resume(ctx, inspected.Container.ID, string(state.Status), "Connect"); err != nil {
			return nil, err
		}
		if err := c.waitUntilRunning(ctx, inspected.Container.ID, "Connect"); err != nil {
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

// dockerStartReadyTimeout bounds how long Create/Connect/Exec wait for PID 1
// after the daemon has accepted a start. The window is milliseconds on a
// healthy daemon; this is only the ceiling for a wedged one.
var dockerStartReadyTimeout = 15 * time.Second

// dockerStartReadyPoll is the inspect interval inside waitUntilRunning.
var dockerStartReadyPoll = 50 * time.Millisecond

// resume brings a paused or stopped container back to running.
func (c *DockerRemoteClient) resume(ctx context.Context, id, status, op string) error {
	if op == "" {
		op = "Connect"
	}
	if strings.EqualFold(status, "paused") {
		if _, err := c.api.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{}); err != nil {
			return dockerError(op, err)
		}
		return nil
	}
	if _, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return dockerError(op, err)
	}
	return nil
}

// dockerContainerNotRunning reports the Engine 409 that ExecCreate returns
// when the target is not in State.Running. The message is the signal: Kind
// Conflict also covers "already exists" and is not replaceable, so callers
// that want to resume have to look here rather than at CanReplaceRemoteBinding.
func dockerContainerNotRunning(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "is not running")
}

// waitUntilRunning polls inspect until the container is running, or until it
// is clear waiting will not help (exited, paused, dead).
func (c *DockerRemoteClient) waitUntilRunning(ctx context.Context, id, op string) error {
	deadline := time.Now().Add(dockerStartReadyTimeout)
	var lastStatus string
	for {
		if err := ctx.Err(); err != nil {
			return dockerError(op, err)
		}
		inspected, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err != nil {
			return dockerError(op, err)
		}
		state := inspected.Container.State
		if state == nil {
			return dockerError(op, errors.New("daemon returned no container state"))
		}
		lastStatus = strings.TrimSpace(string(state.Status))
		switch dockerStateOf(state.Status) {
		case RemoteStateRunning:
			return nil
		case RemoteStateTerminal:
			return &RemoteError{
				Kind:     RemoteErrorKindTerminal,
				Provider: SandboxTypeDocker,
				Op:       op,
				Message:  "container is dead",
			}
		}
		switch strings.ToLower(lastStatus) {
		case "exited", "paused":
			return &RemoteError{
				Kind:     RemoteErrorKindConflict,
				Provider: SandboxTypeDocker,
				Op:       op,
				Message:  "container is not running: " + lastStatus,
			}
		}
		if !time.Now().Before(deadline) {
			return &RemoteError{
				Kind:     RemoteErrorKindTimeout,
				Provider: SandboxTypeDocker,
				Op:       op,
				Message:  fmt.Sprintf("container did not reach running (last state %q)", lastStatus),
			}
		}
		timer := time.NewTimer(dockerStartReadyPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return dockerError(op, ctx.Err())
		case <-timer.C:
		}
	}
}

// ensureRunning resumes a stopped/paused container and waits until exec can
// succeed. It is the Exec counterpart of Connect: the first command of a
// newly created sandbox used to race PID 1, and a later command can hit a
// container the host or the idle sweep stopped.
func (c *DockerRemoteClient) ensureRunning(ctx context.Context, id, op string) error {
	inspected, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return dockerError(op, err)
	}
	state := inspected.Container.State
	if state == nil {
		return dockerError(op, errors.New("daemon returned no container state"))
	}
	switch dockerStateOf(state.Status) {
	case RemoteStateRunning:
		return nil
	case RemoteStateTerminal:
		return &RemoteError{
			Kind:     RemoteErrorKindTerminal,
			Provider: SandboxTypeDocker,
			Op:       op,
			Message:  "container is dead",
		}
	case RemoteStatePaused:
		if err := c.resume(ctx, inspected.Container.ID, string(state.Status), op); err != nil {
			return err
		}
		return c.waitUntilRunning(ctx, id, op)
	default:
		return c.waitUntilRunning(ctx, id, op)
	}
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

	execOpts := client.ExecCreateOptions{
		Cmd:          dockerExecCommand(req, timeout),
		User:         dockerExecUser(req.User),
		WorkingDir:   req.WorkDir,
		Env:          dockerEnvSlice(req.Env),
		AttachStdin:  req.Stdin != "",
		AttachStdout: true,
		AttachStderr: true,
	}
	created, err := c.api.ExecCreate(execCtx, id, execOpts)
	if err != nil && dockerContainerNotRunning(err) {
		if readyErr := c.ensureRunning(execCtx, id, "Exec"); readyErr != nil {
			return nil, readyErr
		}
		created, err = c.api.ExecCreate(execCtx, id, execOpts)
	}
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
// A blank user resolves to the sandbox account. It must never resolve to root:
// this function is the single choke point for every exec the daemon runs, so a
// caller that forgets to name an account has to lose privileges here, not
// silently gain them. It also makes the backends agree — E2B authenticates its
// data plane as DefaultSandboxExecUser and Cube hands a blank field to envd,
// which defaults the same way.
//
// This used to fall back to root for the manager's artifact-directory
// bootstrap. That was a container-escape primitive: chown follows symlinks, so
// a session that replaced its own artifact directory with a link to /etc got
// the root-run bootstrap to hand it ownership of /etc, and from there uid 0 by
// rewriting passwd. The bootstrap now names the account like everyone else and
// simply fails when it is aimed at something the account does not own.
func dockerExecUser(user string) string {
	if trimmed := strings.TrimSpace(user); trimmed != "" {
		return trimmed
	}
	return DefaultSandboxExecUser
}

// dockerExecWasKilled reports whether an exit code means the wrapper killed
// the process. 137 is SIGKILL (timeout -s KILL), 124 is timeout(1) reporting
// that it had to intervene.
func dockerExecWasKilled(exitCode int) bool {
	return exitCode == 137 || exitCode == 124
}

// WriteFile writes one file as the sandbox account.
//
// This and its Read/Stat counterparts deliberately avoid the Engine's archive
// endpoints (CopyToContainer, CopyFromContainer, ContainerStatPath). Those are
// served by the daemon, which means two things at once: they ignore the exec
// user and act as root, and they resolve symlinks on the way. That combination
// is unsafe here, because the sandbox account can write anywhere under
// /workspace while every caller-facing path guard in this repository is a
// string prefix test. A model that runs `ln -s /root /workspace/output/esc`
// leaves /workspace/output/esc/secret.txt passing those guards, and the daemon
// then reads it out as root — confirmed against a real daemon, not theorised.
//
// Running these as DefaultSandboxExecUser hands the decision to the kernel
// instead: a path the sandbox account cannot reach on its own stays
// unreachable regardless of what a link points at, and there is no window
// between checking and using in which the link could be repointed.
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

	// The path travels as a positional argument so a name containing shell
	// metacharacters cannot change what the redirect targets.
	result, err := c.Exec(ctx, &dockerSandboxHandle{id: id}, RemoteExecRequest{
		Command: "sh",
		Args:    []string{"-c", `cat > "$1"`, "weknora-write", clean},
		Stdin:   string(content),
		User:    DefaultSandboxExecUser,
		Timeout: dockerFilesystemOpTimeout,
	})
	if err != nil {
		return dockerError("WriteFile", err)
	}
	if result.ExitCode != 0 {
		return dockerFileOpError("WriteFile", clean, result.Stderr)
	}
	return nil
}

// dockerSandboxPID1User overrides the image USER so the entrypoint can create
// the world-writable activity marker. Numeric 0 does not depend on a root
// account existing by name in a custom image.
const dockerSandboxPID1User = "0"

// ReadFile reads one file as the sandbox account. See WriteFile for why this
// does not use the archive endpoint.
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
	result, err := c.Exec(ctx, &dockerSandboxHandle{id: id}, RemoteExecRequest{
		Command: "cat",
		Args:    []string{"--", clean},
		User:    DefaultSandboxExecUser,
		Timeout: dockerFilesystemOpTimeout,
	})
	if err != nil {
		return nil, dockerError("ReadFile", err)
	}
	if result.ExitCode != 0 {
		return nil, dockerFileOpError("ReadFile", clean, result.Stderr)
	}
	return []byte(result.Stdout), nil
}

// Stat returns metadata for one path.
//
// find reports the FINAL component without following it, so a path that names a
// link reports RemoteEntryOther rather than whatever it points at, and callers
// that only accept regular files refuse it before any read is attempted.
//
// That guarantee stops at the final component. Intermediate components are
// resolved by the kernel during path lookup, exactly as they are for any other
// process, so `/workspace/output/link-to-etc/passwd` stats as a regular file —
// verified against a real daemon. Reads through such a path are not a
// privilege boundary being crossed, only the "stay inside the artifact
// directory" convention: ReadFile still runs as the sandbox account, so it
// returns what that account could have read anyway with shell_exec.
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
	result, err := c.Exec(ctx, &dockerSandboxHandle{id: id}, RemoteExecRequest{
		Command: "find",
		Args: []string{
			clean, "-maxdepth", "0",
			"-printf", `%y\t%s\t%T@\t%p\n`,
		},
		User:    DefaultSandboxExecUser,
		Timeout: dockerFilesystemOpTimeout,
	})
	if err != nil {
		return nil, dockerError("Stat", err)
	}
	if result.ExitCode != 0 {
		return nil, dockerFileOpError("Stat", clean, result.Stderr)
	}
	entries := parseDockerFindOutput(result.Stdout)
	if len(entries) == 0 {
		return nil, &RemoteError{
			Kind:     RemoteErrorKindNotFound,
			Provider: SandboxTypeDocker,
			Op:       "Stat",
			Message:  clean + " does not exist",
		}
	}
	return &RemoteStatEntry{
		Path:    entries[0].Path,
		Type:    entries[0].Type,
		Size:    entries[0].Size,
		ModTime: entries[0].ModTime,
	}, nil
}

// dockerFileOpError classifies a failed filesystem helper. A missing path is
// NotFound so callers can treat it as "nothing there"; everything else,
// permission denials included, is an invalid request carrying the tool's own
// complaint rather than a synthesised one.
func dockerFileOpError(op, clean, stderr string) error {
	if strings.Contains(stderr, "No such file or directory") {
		return &RemoteError{
			Kind:     RemoteErrorKindNotFound,
			Provider: SandboxTypeDocker,
			Op:       op,
			Message:  clean + " does not exist",
		}
	}
	return &RemoteError{
		Kind:     RemoteErrorKindInvalidRequest,
		Provider: SandboxTypeDocker,
		Op:       op,
		Message:  fmt.Sprintf("%s %s: %s", op, clean, firstNonEmptyLine(stderr)),
	}
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
	// Skill snapshots are daemon-local commits, not registry tags. Pulling
	// one would hit Docker Hub for a name we minted and never pushed, and a
	// miss here means "this daemon does not have the image", not "fetch it".
	if dockerIsSkillSnapshotRef(image) {
		return dockerInvalidRequest("Create", "skill snapshot image "+image+" is not on this daemon")
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
// File operations run as the sandbox account (see WriteFile), so the kernel
// already decides what is reachable and this list is not what keeps /etc or
// another session's data safe. It exists for the paths the sandbox account
// legitimately can touch but never should through this API: /proc and /sys
// expose the container's own runtime state, and the activity marker is the
// sweeper's bookkeeping, which a session must not be able to backdate.
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

var (
	_ RemoteSandboxClient   = (*DockerRemoteClient)(nil)
	_ RemoteSnapshotManager = (*DockerRemoteClient)(nil)
)
