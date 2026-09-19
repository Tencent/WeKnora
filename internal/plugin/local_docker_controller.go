package plugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultRuntimeGID is the fixed supplementary group shared by the app and the
// runtime-agent so that Unix sockets and directories are group-accessible
// across the container boundary without either side running as root.
const DefaultRuntimeGID = 2000

// LocalDockerRuntimeController is the in-process PluginRuntimeController. It
// runs the docker CLI directly, owns the socket directories and the per-plugin
// egress proxy. This is the dev / single-process form and the execution backend
// used by the runtime-agent; production app containers use
// RemoteRuntimeAgentController instead.
type LocalDockerRuntimeController struct {
	DockerBinary string
	SocketDir    string
	AuditSink    AuditSink
	// RuntimeGID is the shared runtime group applied to socket directories
	// (setgid) and sockets so the app container's non-root user can traverse
	// and connect. Zero disables the group adjustment (dev on Windows).
	RuntimeGID int
	// CPULimit is the --cpus value passed to docker run ("1", "0.5", ...).
	CPULimit string
	// InstanceID labels containers so a restarted agent only cleans up its own.
	InstanceID string

	mu            sync.Mutex
	cmd           *exec.Cmd
	egressProxy   *EgressProxy
	containerName string
	dir           string
	handle        *PluginHandle
}

func NewLocalDockerRuntimeController(socketDir string, auditSink AuditSink) *LocalDockerRuntimeController {
	return &LocalDockerRuntimeController{
		DockerBinary: "docker",
		SocketDir:    socketDir,
		AuditSink:    auditSink,
		RuntimeGID:   DefaultRuntimeGID,
		CPULimit:     "1",
		InstanceID:   "weknora",
	}
}

func (c *LocalDockerRuntimeController) Start(ctx context.Context, req StartPluginRequest) (handle *PluginHandle, err error) {
	if req.Image == "" {
		return nil, fmt.Errorf("plugin %q has no OCI image", req.PluginID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	docker := c.DockerBinary
	if docker == "" {
		docker = "docker"
	}
	// The controller owns the directory lifecycle: a fixed SocketDir (set by the
	// runtime-agent under its shared runtime root) is created and later removed
	// exactly like a temporary one.
	dir := c.SocketDir
	if dir == "" {
		var mkErr error
		dir, mkErr = os.MkdirTemp("", "weknora-plugin-")
		if mkErr != nil {
			return nil, mkErr
		}
		c.SocketDir = dir
	}
	var egressProxy *EgressProxy
	defer func() {
		if err != nil {
			if egressProxy != nil {
				_ = egressProxy.Stop(context.Background())
			}
			if dir != "" {
				_ = os.RemoveAll(dir)
				if c.SocketDir == dir {
					c.SocketDir = ""
				}
			}
		}
	}()
	if err := c.ensureDir(dir); err != nil {
		return nil, err
	}
	controlDir := filepath.Join(dir, "control")
	if err := c.ensureDir(controlDir); err != nil {
		return nil, err
	}
	hostSocket := filepath.Join(controlDir, "plugin.sock")
	containerSocket := "/run/weknora/plugin.sock"
	if req.NetworkPolicy != NetworkNone {
		egressDir := filepath.Join(dir, "egress")
		if err := c.ensureDir(egressDir); err != nil {
			return nil, err
		}
		egressProxy = NewEgressProxy(
			filepath.Join(egressDir, "egress.sock"),
			req.PluginID,
			req.NetworkPolicy,
			req.AllowedDestinations,
			c.AuditSink,
		)
		if err := egressProxy.Start(); err != nil {
			return nil, fmt.Errorf("start plugin egress proxy: %w", err)
		}
	}
	containerName := pluginContainerName(c.InstanceID, req.PluginID)
	args := c.dockerArgs(req, dir, containerSocket, containerName)
	cmd := exec.Command(docker, args...)
	var stderr io.ReadCloser
	if c.AuditSink != nil {
		p, perr := cmd.StderrPipe()
		if perr != nil {
			return nil, fmt.Errorf("capture docker plugin stderr: %w", perr)
		}
		stderr = p
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start docker plugin: %w", err)
	}
	if stderr != nil {
		go ConsumePluginStderr(stderr, req.PluginID, c.AuditSink)
	}
	// Wait for the plugin to create its control socket and hand its ownership
	// to the shared runtime group — without this the socket is root:root 0755
	// and the app container's non-root user cannot connect to it.
	if err := c.handoverControlSocket(hostSocket, req.PluginID); err != nil {
		return nil, err
	}
	handle = &PluginHandle{PluginID: req.PluginID, ControlSocket: hostSocket, ContainerName: containerName}
	c.cmd = cmd
	c.egressProxy = egressProxy
	c.containerName = containerName
	c.dir = dir
	c.handle = handle
	egressProxy = nil
	return handle, nil
}

// controlSocketHandoverTimeout bounds how long the controller waits for the
// plugin container to create its control socket. It must stay well below the
// app-side dial window (15s) so a slow-but-healthy plugin still connects.
const controlSocketHandoverTimeout = 10 * time.Second

// handoverControlSocket waits for the plugin container to create its control
// socket, then transfers its ownership to the shared runtime group.
//
// Why this is needed: the plugin process runs as root inside its container
// (with --cap-drop ALL it cannot chown on the host side either), so a freshly
// created socket is root:root 0755. Connecting to a Unix socket requires write
// permission on the socket inode, which nobody outside root has — the app
// container's non-root user would get EACCES on every dial. The handover
// (chown to the runtime GID + 0660) restores the designed app↔plugin
// reachability: exactly the members of the shared runtime group (app, agent)
// can connect, nobody else. The handover is recorded in the audit sink.
func (c *LocalDockerRuntimeController) handoverControlSocket(hostSocket, pluginID string) error {
	deadline := time.Now().Add(controlSocketHandoverTimeout)
	for {
		if _, err := os.Stat(hostSocket); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat plugin control socket: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("plugin control socket %q did not appear within %s", hostSocket, controlSocketHandoverTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if c.RuntimeGID > 0 {
		if err := os.Chown(hostSocket, -1, c.RuntimeGID); err != nil {
			return fmt.Errorf("chown plugin control socket: %w", err)
		}
	}
	if err := os.Chmod(hostSocket, 0o660); err != nil {
		return fmt.Errorf("chmod plugin control socket: %w", err)
	}
	if c.AuditSink != nil {
		c.AuditSink.Record(AuditEvent{
			PluginID:    pluginID,
			Action:      "socket-handover",
			Destination: hostSocket,
			Allowed:     true,
			Reason:      "control socket ownership transferred to runtime group",
			At:          time.Now().UTC(),
		})
	}
	return nil
}

// ensureDir creates a directory with group traverse/setgid permissions so a
// non-root app user in the shared runtime group can reach sockets inside it.
func (c *LocalDockerRuntimeController) ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if c.RuntimeGID <= 0 {
		return nil
	}
	if err := os.Chown(dir, -1, c.RuntimeGID); err != nil {
		return err
	}
	return os.Chmod(dir, 0o2750) // setgid + rwxr-x---
}

// pluginContainerName derives the deterministic container name. It embeds the
// runtime instance ID so two deployments sharing one Docker daemon (or an agent
// whose instance ID changed) never collide on the same name. Both parts are
// sanitized into the legal container-name character set and lower-cased.
func pluginContainerName(instanceID, pluginID string) string {
	var b strings.Builder
	b.WriteString("weknora-plugin-")
	if instanceID != "" {
		b.WriteString(sanitizeContainerNamePart(instanceID))
		b.WriteByte('-')
	}
	b.WriteString(sanitizeContainerNamePart(pluginID))
	return b.String()
}

// sanitizeContainerNamePart lower-cases and maps a value into the legal Docker
// container-name character set ([a-z0-9_.-]).
func sanitizeContainerNamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (c *LocalDockerRuntimeController) dockerArgs(req StartPluginRequest, dir, containerSocket, containerName string) []string {
	controlDir := filepath.Join(dir, "control")
	args := []string{"run", "--rm", "--name", containerName, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "128", "--memory", "512m"}
	if c.CPULimit != "" {
		args = append(args, "--cpus", c.CPULimit)
	}
	if c.InstanceID != "" {
		args = append(args, "--label", "weknora.plugin.runtime="+c.InstanceID)
	}
	args = append(args,
		"--label", "weknora.plugin.id="+req.PluginID,
		"-v", controlDir+":/run/weknora:rw",
		"-e", "WEKNORA_PLUGIN_ADDR=unix://"+containerSocket,
		"-e", "WEKNORA_PLUGIN_ID="+req.PluginID,
		"-e", "WEKNORA_PLUGIN_PROTOCOL_VERSION="+req.ProtocolVersion,
		"-e", "WEKNORA_PLUGIN_NETWORK_POLICY="+string(req.NetworkPolicy),
		"-e", "WEKNORA_PLUGIN_NETWORK_ALLOWLIST="+strings.Join(req.AllowedDestinations, ","),
	)
	if req.NetworkPolicy != NetworkNone {
		egressDir := filepath.Join(dir, "egress")
		args = append(args, "-v", egressDir+":/run/weknora-egress:ro", "-e", "WEKNORA_PLUGIN_EGRESS_SOCKET=/run/weknora-egress/egress.sock")
	}
	return append(args, req.Image)
}

// dockerStopGrace is the container-level SIGTERM window granted by
// `docker stop -t` before Docker force-kills the container.
const dockerStopGrace = 5 * time.Second

func (c *LocalDockerRuntimeController) Stop(ctx context.Context, pluginID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	cmd, dir, container, egressProxy := c.cmd, c.dir, c.containerName, c.egressProxy
	c.cmd, c.egressProxy = nil, nil
	c.containerName = ""
	c.dir = ""
	c.handle = nil
	c.mu.Unlock()
	if container != "" {
		docker := c.DockerBinary
		if docker == "" {
			docker = "docker"
		}
		stopCtx, cancel := context.WithTimeout(ctx, dockerStopGrace+10*time.Second)
		defer cancel()
		graceSeconds := fmt.Sprintf("%d", int(dockerStopGrace.Seconds()))
		stop := exec.CommandContext(stopCtx, docker, "stop", "-t", graceSeconds, container)
		if err := stop.Run(); err != nil && cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Wait()
	}
	if egressProxy != nil {
		_ = egressProxy.Stop(ctx)
	}
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}

// CurrentHandle returns the handle recorded by the last successful Start, or
// nil. It backs idempotent Start semantics in the runtime-agent.
func (c *LocalDockerRuntimeController) CurrentHandle() *PluginHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handle
}

// ForceStopByLabels force-removes any container of this instance for the given
// plugin, regardless of in-memory state. It matches BOTH the runtime instance
// label and the plugin label, so an agent never removes another deployment's
// container for a same-ID plugin. It is the recovery path for an agent that
// restarted and lost its controller map while containers kept running.
func (c *LocalDockerRuntimeController) ForceStopByLabels(ctx context.Context, pluginID string) error {
	filters := []string{"label=weknora.plugin.id=" + pluginID}
	if c.InstanceID != "" {
		filters = append(filters, "label=weknora.plugin.runtime="+c.InstanceID)
	}
	return c.removeContainersByLabels(ctx, filters...)
}

// CleanupOrphaned force-removes every container labeled with this instance ID.
// Called on agent startup so a crashed agent does not leave running containers
// that later collide on deterministic names.
func (c *LocalDockerRuntimeController) CleanupOrphaned(ctx context.Context) error {
	if c.InstanceID == "" {
		return nil
	}
	return c.removeContainersByLabels(ctx, "label=weknora.plugin.runtime="+c.InstanceID)
}

func (c *LocalDockerRuntimeController) removeContainersByLabels(ctx context.Context, labelFilters ...string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	docker := c.DockerBinary
	if docker == "" {
		docker = "docker"
	}
	args := []string{"ps", "-aq"}
	for _, f := range labelFilters {
		args = append(args, "--filter", f)
	}
	list := exec.CommandContext(ctx, docker, args...)
	out, err := list.Output()
	if err != nil {
		return fmt.Errorf("list containers by label: %w", err)
	}
	for _, id := range strings.Fields(string(out)) {
		_ = exec.CommandContext(ctx, docker, "rm", "-f", id).Run()
	}
	return nil
}
