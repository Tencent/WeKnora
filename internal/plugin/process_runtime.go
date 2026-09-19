package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ProcessRuntime is the development/desktop runtime. It deliberately has no
// claim of security isolation: use DockerRuntime or another constrained agent
// in a server deployment. Both runtimes implement the same lifecycle surface.
//
// ProcessRuntime depends only on the shared control-plane client
// (pluginapi.PluginControlClient). It never holds type-specific protocol
// clients, so adding a new extension type requires no change here.
// maxPluginMsgSize caps the gRPC message size between the host and plugin
// processes. It matches the docreader's 50MB limit so image-heavy documents
// survive the unary parser protocol.
const maxPluginMsgSize = 50 * 1024 * 1024

type ProcessRuntime struct {
	Manifest Manifest
	Command  string
	Args     []string
	Address  string

	mu            sync.RWMutex
	cmd           *exec.Cmd
	conn          *grpc.ClientConn
	controlClient pluginapi.PluginControlClient
	AuditSink     AuditSink
}

func NewProcessRuntime(manifest Manifest, command string, args ...string) *ProcessRuntime {
	return &ProcessRuntime{Manifest: manifest, Command: command, Args: append([]string(nil), args...), AuditSink: LoggerAuditSink{}}
}

func (r *ProcessRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.cmd != nil && r.clientReadyLocked() {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	if r.Command == "" {
		return fmt.Errorf("plugin %q has no process command", r.Manifest.ID)
	}

	// Keep the transport as loopback TCP to stay cross-platform with the
	// desktop edition (cmd/desktop). Probing a free port and closing it before
	// the child binds has a small TOCTOU window, so retry a few times to cover
	// the rare case where another process steals the port in between.
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		address := r.Address
		if address == "" {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return err
			}
			address = listener.Addr().String()
			_ = listener.Close()
		}
		if err := r.startOnce(ctx, address); err != nil {
			lastErr = err
			if attempt < maxAttempts-1 {
				// Exponential backoff between attempts so a transient port
				// race does not exhaust all attempts within a few milliseconds.
				delay := 100 * time.Millisecond * time.Duration(1<<uint(attempt))
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			continue
		}
		return nil
	}
	return lastErr
}

func (r *ProcessRuntime) startOnce(ctx context.Context, address string) error {
	commandArgs := append([]string(nil), r.Args...)
	cmd := exec.Command(r.Command, commandArgs...)
	cmd.Env = append(os.Environ(),
		"WEKNORA_PLUGIN_ADDR="+address,
		"WEKNORA_PLUGIN_ID="+r.Manifest.ID,
		"WEKNORA_PLUGIN_PROTOCOL_VERSION="+r.Manifest.ProtocolVersion,
		"WEKNORA_PLUGIN_NETWORK_POLICY="+string(r.Manifest.EffectiveNetworkPolicy()),
		"WEKNORA_PLUGIN_NETWORK_ALLOWLIST="+strings.Join(r.Manifest.Permissions.AllowedDestinations, ","),
	)
	var stderr io.ReadCloser
	if r.AuditSink != nil {
		p, perr := cmd.StderrPipe()
		if perr != nil {
			return fmt.Errorf("capture plugin stderr: %w", perr)
		}
		stderr = p
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start plugin process: %w", err)
	}
	if stderr != nil {
		go ConsumePluginStderr(stderr, r.Manifest.ID, r.AuditSink)
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(connectCtx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxPluginMsgSize), grpc.MaxCallSendMsgSize(maxPluginMsgSize),
		),
	)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("connect plugin process: %w", err)
	}
	controlClient := pluginapi.NewPluginControlClient(conn)
	handshakeWire, err := controlClient.Handshake(connectCtx, &pluginproto.HandshakeRequest{})
	if err != nil {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("plugin handshake: %w", err)
	}
	if _, err := validateHandshake(r.Manifest, handshakeWire); err != nil {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	r.mu.Lock()
	r.Address, r.cmd, r.conn, r.controlClient = address, cmd, conn, controlClient
	r.mu.Unlock()
	return nil
}

// processStopGrace bounds how long Stop waits for the plugin process to exit
// after cancellation and a termination signal, before force-killing it.
const processStopGrace = 5 * time.Second

// Stop cancels in-flight calls, requests a graceful exit, and force-kills the
// process only after a bounded grace period: closing the gRPC connection makes
// the plugin's handlers observe context cancellation (so a well-behaved
// long-task handler can flush state or cancel remote work), the interrupt
// signal lets the SDK's graceful-shutdown path run, and the kill fallback
// guarantees termination for handlers that ignore both.
func (r *ProcessRuntime) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	cmd, conn := r.cmd, r.conn
	r.cmd, r.conn, r.controlClient = nil, nil, nil
	r.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// A caller-provided deadline wins over the local grace period:
	// context.WithTimeout keeps whichever expires first.
	stopCtx, cancel := context.WithTimeout(ctx, processStopGrace)
	defer cancel()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// Best effort: signal delivery is unsupported on some platforms (e.g.
	// Windows), where the kill fallback below remains the enforcement path.
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case <-exited:
		return nil
	case <-stopCtx.Done():
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		<-exited
		return nil
	}
}

func (r *ProcessRuntime) Health(ctx context.Context) HealthStatus {
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
func (r *ProcessRuntime) Conn() *grpc.ClientConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conn
}

func (r *ProcessRuntime) clientReadyLocked() bool {
	return r.controlClient != nil
}

type processConnectorLease struct {
	connector  datasource.Connector
	release    func()
	generation uint64
}

func (l processConnectorLease) Connector() datasource.Connector { return l.connector }
func (l processConnectorLease) Close() error {
	if l.release != nil {
		l.release()
	}
	return nil
}
func (l processConnectorLease) Generation() uint64 { return l.generation }
