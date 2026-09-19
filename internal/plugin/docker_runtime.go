package plugin

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DockerRuntime is the server-side OCI runtime. It talks to the plugin over a
// mounted Unix socket and always starts the container with --network none. If
// the effective policy permits networking, a per-plugin host-side egress proxy
// is mounted beside the control socket; the plugin still has no network
// namespace and can only leave through that proxy.
//
// The actual container launch / socket directory / egress proxy lifecycle is
// delegated to a PluginRuntimeController. The default local controller runs the
// docker CLI in-process (dev / single-process); production separates Docker
// privileges into a runtime-agent via NewDockerRuntimeWithController.
//
// DockerRuntime depends only on the shared control-plane client
// (pluginapi.PluginControlClient), never on type-specific protocol clients.
type DockerRuntime struct {
	Manifest   Manifest
	Image      string
	controller PluginRuntimeController
	AuditSink  AuditSink

	mu            sync.RWMutex
	conn          *grpc.ClientConn
	controlClient pluginapi.PluginControlClient
	containerName string
}

func NewDockerRuntime(manifest Manifest, image string) *DockerRuntime {
	sink := LoggerAuditSink{}
	return &DockerRuntime{
		Manifest:   manifest,
		Image:      image,
		controller: NewLocalDockerRuntimeController("", sink),
		AuditSink:  sink,
	}
}

// NewDockerRuntimeWithController builds a DockerRuntime whose container launch
// and egress lifecycle are handled by the given controller (e.g. a remote
// runtime-agent in production). The runtime still owns the gRPC connection,
// handshake and health probing.
func NewDockerRuntimeWithController(manifest Manifest, image string, controller PluginRuntimeController) *DockerRuntime {
	return &DockerRuntime{
		Manifest:   manifest,
		Image:      image,
		controller: controller,
		AuditSink:  LoggerAuditSink{},
	}
}

func (r *DockerRuntime) startRequest() StartPluginRequest {
	return StartPluginRequest{
		PluginID:            r.Manifest.ID,
		Image:               r.Image,
		ExtensionType:       string(r.Manifest.ExtensionType),
		ProtocolVersion:     r.Manifest.ProtocolVersion,
		NetworkPolicy:       r.Manifest.EffectiveNetworkPolicy(),
		AllowedDestinations: r.Manifest.Permissions.AllowedDestinations,
	}
}

func (r *DockerRuntime) Start(ctx context.Context) (err error) {
	r.mu.RLock()
	ready := r.clientReadyLocked()
	r.mu.RUnlock()
	if ready {
		return nil
	}
	handle, err := r.controller.Start(ctx, r.startRequest())
	if err != nil {
		return err
	}
	// Connect to the plugin's control socket and handshake. On any failure the
	// controller is asked to tear the container down again so a retry is clean.
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dialer := func(dialCtx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(dialCtx, "unix", handle.ControlSocket)
	}
	conn, err := grpc.DialContext(connectCtx, "unix://"+handle.ControlSocket, grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		_ = r.controller.Stop(context.Background(), r.Manifest.ID)
		return fmt.Errorf("connect docker plugin: %w", err)
	}
	controlClient := pluginapi.NewPluginControlClient(conn)
	handshakeWire, err := controlClient.Handshake(connectCtx, &pluginproto.HandshakeRequest{})
	if err != nil {
		_ = conn.Close()
		_ = r.controller.Stop(context.Background(), r.Manifest.ID)
		return fmt.Errorf("docker plugin handshake: %w", err)
	}
	if _, err := validateHandshake(r.Manifest, handshakeWire); err != nil {
		_ = conn.Close()
		_ = r.controller.Stop(context.Background(), r.Manifest.ID)
		return err
	}
	r.mu.Lock()
	r.conn, r.controlClient, r.containerName = conn, controlClient, handle.ContainerName
	r.mu.Unlock()
	return nil
}

func (r *DockerRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	conn := r.conn
	r.conn, r.controlClient = nil, nil
	r.containerName = ""
	r.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	return r.controller.Stop(ctx, r.Manifest.ID)
}

func (r *DockerRuntime) Health(ctx context.Context) HealthStatus {
	r.mu.RLock()
	controlClient := r.controlClient
	r.mu.RUnlock()
	if controlClient == nil {
		return HealthStatus{State: StateStopped, CheckedAt: time.Now().UTC()}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	wire, err := controlClient.Health(checkCtx, &pluginproto.HealthRequest{})
	if err != nil {
		return HealthStatus{State: StateUnhealthy, Message: err.Error(), CheckedAt: time.Now().UTC()}
	}
	var health pluginapi.HealthResponse
	if err := pluginapi.DecodeHealth(wire, &health); err != nil {
		return HealthStatus{State: StateUnhealthy, Message: err.Error(), CheckedAt: time.Now().UTC()}
	}
	state := HealthState(health.State)
	if state == "" {
		state = StateRunning
	}
	return HealthStatus{State: state, Message: health.Message, CheckedAt: time.Now().UTC()}
}

// Conn exposes the raw gRPC connection so protocol adapters can construct their
// own type-specific clients. The runtime keeps only the control-plane client.
func (r *DockerRuntime) Conn() *grpc.ClientConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conn
}

func (r *DockerRuntime) clientReadyLocked() bool {
	return r.controlClient != nil
}
