// Docker Engine plumbing for the docker backend.
//
// This file owns everything between WeKnora and the Docker Engine API that is
// not sandbox semantics: the narrow interface the adapter talks to (so unit
// tests need no daemon), how a daemon connection is built and shared, and how
// Engine errors are classified into the provider-neutral RemoteErrorKind.
//
// Connections are pooled per daemon endpoint. Managers are rebuilt on every
// request (see tenant_resolver.go), and each moby client owns an HTTP
// transport, so constructing one per request would leak a connection pool per
// request.

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// dockerEngineAPI is the slice of the Docker Engine API the sandbox adapter
// uses. It exists so tests can drive the adapter without a daemon; the real
// implementation is *client.Client, which satisfies it as-is.
type dockerEngineAPI interface {
	Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)

	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStart(
		ctx context.Context, containerID string, options client.ContainerStartOptions,
	) (client.ContainerStartResult, error)
	ContainerUnpause(
		ctx context.Context, containerID string, options client.ContainerUnpauseOptions,
	) (client.ContainerUnpauseResult, error)
	ContainerInspect(
		ctx context.Context, containerID string, options client.ContainerInspectOptions,
	) (client.ContainerInspectResult, error)
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerRemove(
		ctx context.Context, containerID string, options client.ContainerRemoveOptions,
	) (client.ContainerRemoveResult, error)

	ExecCreate(
		ctx context.Context, containerID string, options client.ExecCreateOptions,
	) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)

	// ContainerStatPath is the one archive endpoint this adapter uses, and only
	// against the activity marker's fixed path. The copy endpoints are
	// deliberately absent: they run as root and resolve symlinks, so exposing
	// them to caller-supplied paths would undo the file operations' reliance on
	// the kernel for access control (see DockerRemoteClient.WriteFile).
	ContainerStatPath(
		ctx context.Context, containerID string, options client.ContainerStatPathOptions,
	) (client.ContainerStatPathResult, error)

	ImageInspect(
		ctx context.Context, imageID string, opts ...client.ImageInspectOption,
	) (client.ImageInspectResult, error)
	ImagePull(ctx context.Context, refStr string, options client.ImagePullOptions) (client.ImagePullResponse, error)
	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
}

var _ dockerEngineAPI = (*client.Client)(nil)

// DefaultDockerHost is the last-resort daemon endpoint when the config, the
// DOCKER_HOST environment variable, and the current docker CLI context are
// all empty. Linux installs typically expose this socket; macOS tools such
// as Colima do not.
const DefaultDockerHost = "unix:///var/run/docker.sock"

// DefaultDockerHTTPTimeout bounds a single short Engine API call (ping, create,
// inspect, list, delete, exec create/inspect). Streaming calls — image pull,
// exec hijack, archive copy — use the caller's context instead, because
// http.Client.Timeout would kill them mid-body.
const DefaultDockerHTTPTimeout = 30 * time.Second

// DefaultDockerIdleTTL is how long a session container may go without an exec
// before the idle sweep reclaims it. The daemon has no TTL of its own, so this
// is the only thing standing between an abandoned session and a container that
// lives until the host runs out of memory.
const DefaultDockerIdleTTL = 30 * time.Minute

// DefaultDockerMemoryLimit / DefaultDockerCPULimit / DefaultDockerPidsLimit are
// the per-sandbox resource ceilings applied when a config names none. They are
// deliberately larger than the stateless backend's old 256MB/1CPU: a session
// container hosts a whole turn's work (package installs, data processing),
// not one short script.
const (
	DefaultDockerMemoryLimit int64 = 2 * 1024 * 1024 * 1024
	DefaultDockerCPULimit          = 2.0
	DefaultDockerPidsLimit   int64 = 512
)

// dockerSandboxCapabilities are granted back after CapDrop=ALL.
//
// Everything Docker grants by default that a sandbox does not need is left
// dropped (NET_RAW, NET_BIND_SERVICE, MKNOD, SYS_CHROOT, AUDIT_WRITE,
// SETPCAP, SETFCAP). What remains is what a root-run package installer needs:
// CHOWN/DAC_OVERRIDE/FOWNER/FSETID for writing into image-owned directories
// and fixing up ownership, SETUID/SETGID because apt and pip drop privileges
// while unpacking, KILL so a supervisor can stop its own children.
var dockerSandboxCapabilities = []string{
	"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETGID", "SETUID", "KILL",
}

// dockerEngineClientPool hands out one shared client per daemon endpoint.
type dockerEngineClientPool struct {
	mu      sync.Mutex
	clients map[string]*client.Client
}

// sharedDockerEngineClients is process-wide on purpose: two tenant configs
// pointing at the same daemon should share one connection pool, and the check
// endpoint builds throwaway configs that must not each open their own.
var sharedDockerEngineClients = &dockerEngineClientPool{
	clients: make(map[string]*client.Client),
}

// dockerEndpoint is the identity of a daemon connection. Two configs with
// equal endpoints may share a client.
type dockerEndpoint struct {
	Host string

	// TLSCertPath is a directory holding ca.pem / cert.pem / key.pem, the
	// layout Docker's own DOCKER_CERT_PATH uses. Empty means plain HTTP,
	// which is only acceptable for a local unix socket.
	TLSCertPath string

	// AllowPrivate mirrors the config's outbound policy. It is part of the
	// endpoint identity because it changes the dialer this client installs:
	// sharing a pooled client between a permissive and a restrictive config
	// would hand the restrictive one a connection it is not allowed to make.
	AllowPrivate bool

	Timeout time.Duration
}

func (e dockerEndpoint) key() string {
	// Timeout is applied per RPC, not on the HTTP client, so two configs
	// that differ only in HTTP timeout still share one connection pool.
	return fmt.Sprintf("%s|%s|%t", e.Host, e.TLSCertPath, e.AllowPrivate)
}

// get returns the shared client for endpoint, building it on first use.
func (p *dockerEngineClientPool) get(endpoint dockerEndpoint) (*client.Client, error) {
	key := endpoint.key()
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.clients[key]; ok {
		return existing, nil
	}
	built, err := newDockerEngineClient(endpoint)
	if err != nil {
		return nil, err
	}
	p.clients[key] = built
	return built, nil
}

// newDockerEngineClient builds a moby client for one endpoint.
//
// API version negotiation is deliberately not performed here: it costs a
// round-trip against a daemon that may be down, and it would happen inside
// whatever request first touches the pool. The client's default version is
// negotiated lazily by the moby client itself on the first call.
func newDockerEngineClient(endpoint dockerEndpoint) (*client.Client, error) {
	host := strings.TrimSpace(endpoint.Host)
	if host == "" {
		host = DetectLocalDockerHost()
	}

	// Do not set http.Client.Timeout. It covers the entire response body,
	// so a cold image pull or a file copy longer than the RPC budget is
	// killed mid-stream. Short calls are bounded by withDockerRPCTimeout;
	// pulls, exec hijacks and archive streams honour the caller's context.
	opts := []client.Opt{
		client.WithHost(host),
	}
	if endpoint.TLSCertPath != "" {
		// Certificates stay on the application host rather than in the
		// workspace config: they are deployment infrastructure, and keeping
		// them out of the database keeps them out of backups and API
		// responses. The daemon certificate is always verified — a remote
		// daemon accepts container creation, so an unauthenticated peer on
		// that socket is a root shell on the sandbox host.
		certPath := endpoint.TLSCertPath
		opts = append(opts, client.WithTLSClientConfig(
			filepath.Join(certPath, "ca.pem"),
			filepath.Join(certPath, "cert.pem"),
			filepath.Join(certPath, "key.pem"),
		))
	}

	// A TCP daemon address is a tenant-supplied endpoint like any other, so it
	// gets the same dial-time guard every other backend gets: saving the config
	// validates the address it was given, but a hostname can resolve to a public
	// address then and to 169.254.169.254 when the connection is actually made.
	// Reaching a private daemon stays possible through the config's own
	// "allow private endpoints" switch. Must come after WithHost, which
	// installs its own dialer.
	if dockerHostNeedsDialGuard(host) {
		opts = append(opts, client.WithDialContext((&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   SafeDialControlForPolicy(OutboundURLPolicy{AllowPrivate: endpoint.AllowPrivate}),
		}).DialContext))
	}

	built, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("sandbox: build docker client for %s: %w", host, err)
	}
	return built, nil
}

// dockerHostNeedsDialGuard reports whether host is a network endpoint whose
// dials must pass the outbound policy. Unix sockets are local to the WeKnora
// process and carry no address to check.
func dockerHostNeedsDialGuard(host string) bool {
	scheme, _, found := strings.Cut(strings.TrimSpace(host), "://")
	if !found {
		return false
	}
	switch strings.ToLower(scheme) {
	case "tcp", "http", "https":
		return true
	default:
		return false
	}
}

// ValidateDockerHost checks a daemon endpoint before it is stored or dialled.
//
// A TCP endpoint gets the same outbound treatment as any other workspace-
// supplied URL: a daemon socket accepts container creation, so an admin who
// can point it anywhere can make WeKnora talk to an arbitrary internal
// service. Unix sockets are local by definition and only have to be absolute.
func ValidateDockerHost(host string, allowPrivate bool) error {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return nil
	}
	scheme, address, found := strings.Cut(trimmed, "://")
	if !found {
		return fmt.Errorf(
			"sandbox: docker host %q must include a scheme (unix:// or tcp://)", host)
	}
	switch strings.ToLower(scheme) {
	case "unix":
		if !strings.HasPrefix(address, "/") {
			return fmt.Errorf("sandbox: docker unix socket path %q must be absolute", address)
		}
		return nil
	case "tcp", "http", "https":
		// The guard speaks HTTP; the daemon's TCP endpoint is an HTTP
		// endpoint, so the check is the same one every other backend gets.
		return ValidateOutboundURLWithPolicy(
			"http://"+address, OutboundURLPolicy{AllowPrivate: allowPrivate},
		)
	default:
		return fmt.Errorf("sandbox: unsupported docker host scheme %q", scheme)
	}
}

// ValidateDockerRemoteTLS requires client certificates for a TCP daemon.
// A remote Engine API that accepts container creation is a root shell on
// that host; plaintext tcp://2375 is not an acceptable way to reach it.
// Unix sockets are local to the WeKnora process and do not use TLS.
func ValidateDockerRemoteTLS(host, tlsCertPath string) error {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return nil
	}
	scheme, _, found := strings.Cut(trimmed, "://")
	if !found {
		return nil
	}
	switch strings.ToLower(scheme) {
	case "tcp", "http", "https":
		if strings.TrimSpace(tlsCertPath) == "" {
			return fmt.Errorf(
				"sandbox: remote docker host %q requires a TLS certificate directory", host)
		}
	}
	return nil
}

// ValidateDockerNetworkMode allows only bridge (egress) and none (no egress).
//
// host and container: modes share another namespace outright, which would put
// sandbox code on the WeKnora host's or a sibling container's network. A
// user-defined network name is refused for the weaker but equally real version
// of the same problem: the usual deployment reaches its daemon through the
// mounted docker.sock, so naming the deployment's own compose network would
// place a sandbox on the same L3 network as Postgres and Redis. Only the
// operator can judge what a given named network exposes, and this value is set
// per workspace config, so it is not theirs to choose.
func ValidateDockerNetworkMode(mode string) error {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		return nil
	}
	switch strings.ToLower(trimmed) {
	case "bridge", "none":
		return nil
	}
	return fmt.Errorf(
		"sandbox: docker network mode %q is not allowed; use \"bridge\" or \"none\"",
		mode)
}

// dockerErrorKind classifies an Engine API error. The moby client tags its
// errors with containerd's errdefs, which is a far more reliable signal than
// the message text.
func dockerErrorKind(op string, err error) RemoteErrorKind {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded), cerrdefs.IsDeadlineExceeded(err):
		return RemoteErrorKindTimeout
	case cerrdefs.IsNotFound(err):
		// A missing image on create is a bad template, not a vanished sandbox:
		// classifying it as NotFound would tell the lifecycle it may rebind.
		if op == "Create" {
			return RemoteErrorKindInvalidRequest
		}
		return RemoteErrorKindNotFound
	case cerrdefs.IsUnauthorized(err), cerrdefs.IsPermissionDenied(err):
		return RemoteErrorKindAuthentication
	case cerrdefs.IsInvalidArgument(err):
		return RemoteErrorKindInvalidRequest
	case cerrdefs.IsNotImplemented(err):
		return RemoteErrorKindUnsupported
	case cerrdefs.IsConflict(err), cerrdefs.IsAlreadyExists(err):
		return RemoteErrorKindConflict
	case cerrdefs.IsResourceExhausted(err):
		return RemoteErrorKindCapacity
	case cerrdefs.IsUnavailable(err), client.IsErrConnectionFailed(err):
		return RemoteErrorKindUnavailable
	default:
		return RemoteErrorKindInternal
	}
}

// dockerError wraps an Engine API error as a RemoteError.
func dockerError(op string, err error) error {
	if err == nil {
		return nil
	}
	var existing *RemoteError
	if errors.As(err, &existing) {
		return err
	}
	return &RemoteError{
		Kind:     dockerErrorKind(op, err),
		Provider: SandboxTypeDocker,
		Op:       op,
		Message:  err.Error(),
		Cause:    err,
	}
}

// dockerInvalidRequest reports a caller-side mistake that never reached the
// daemon (an unusable path, an unsupported request shape).
func dockerInvalidRequest(op, message string) error {
	return &RemoteError{
		Kind:     RemoteErrorKindInvalidRequest,
		Provider: SandboxTypeDocker,
		Op:       op,
		Message:  message,
	}
}

// awaitImagePull waits for a pull to finish. The daemon only performs the
// transfer while its progress stream is being consumed, so a caller that
// closes the body early aborts the pull.
func awaitImagePull(ctx context.Context, body client.ImagePullResponse) error {
	if body == nil {
		return nil
	}
	defer func() { _ = body.Close() }()
	return body.Wait(ctx)
}

// dockerStateOf normalizes a container state string. "exited" is deliberately
// NOT terminal: a stopped container keeps its filesystem and Connect restarts
// it, which is the closest Docker gets to E2B's pause + auto-resume.
func dockerStateOf(status container.ContainerState) RemoteSandboxState {
	switch strings.ToLower(strings.TrimSpace(string(status))) {
	case "running":
		return RemoteStateRunning
	case "paused", "exited", "created":
		return RemoteStatePaused
	case "restarting", "removing":
		return RemoteStateTransitioning
	case "dead":
		return RemoteStateTerminal
	case "":
		return RemoteStateUnknown
	default:
		return RemoteStateUnknown
	}
}

// dockerContainerLabels projects sandbox metadata onto container labels and
// stamps the ownership marker every sweep relies on.
func dockerContainerLabels(metadata map[string]string) map[string]string {
	labels := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		labels[key] = value
	}
	labels[dockerManagedLabel] = "true"
	return labels
}

// dockerSandboxMetadata is the inverse of dockerContainerLabels: it strips the
// ownership marker so callers see exactly the metadata they supplied.
func dockerSandboxMetadata(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	metadata := make(map[string]string, len(labels))
	for key, value := range labels {
		if key == dockerManagedLabel {
			continue
		}
		metadata[key] = value
	}
	return metadata
}

// dockerManagedLabel marks every container this backend creates. Sweeps filter
// on it so a WeKnora deployment sharing a daemon with other workloads can
// never delete a container it does not own.
const dockerManagedLabel = "com.weknora.sandbox.managed"

// dockerContainerStartedAt parses the daemon's RFC3339Nano timestamps, which
// are the zero value string "0001-01-01T00:00:00Z" when unset.
func dockerContainerStartedAt(state *container.State) time.Time {
	if state == nil {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, state.StartedAt)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
