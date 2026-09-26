// Package driver defines how plugin code is run and reached. A driver turns a
// plugin's manifest into running instances (a child process of the plugin
// host, a remote URL, a Kubernetes Deployment) and tells callers where to send
// a request. Only the builtin driver exists today; the interface is fixed now
// so the host, remote and kubernetes drivers slot in without touching callers.
package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

// EndpointKind says how to reach a plugin instance.
type EndpointKind string

const (
	// EndpointInProcess means the code is compiled into this process.
	EndpointInProcess EndpointKind = "in-process"
	// EndpointUnix is a unix socket of a child process on this node.
	EndpointUnix EndpointKind = "unix"
	// EndpointHTTP is an HTTP(S) base URL (plugin host gateway or remote).
	EndpointHTTP EndpointKind = "http"
)

// Endpoint is where one request to a plugin goes.
type Endpoint struct {
	Kind    EndpointKind `json:"kind"`
	Address string       `json:"address,omitempty"`
}

// InstanceState is the health of one running instance.
type InstanceState string

// Instance states, from launch to shutdown.
const (
	// StateStarting: launched, not yet passing health checks.
	StateStarting InstanceState = "starting"
	// StateReady: serving requests.
	StateReady InstanceState = "ready"
	// StateDegraded: running but failing health checks.
	StateDegraded InstanceState = "degraded"
	// StateStopped: not running.
	StateStopped InstanceState = "stopped"
)

// EgressMode is how an instance's outbound traffic is held to the
// manifest's permissions.egress.
type EgressMode string

// Egress modes, from enforced to not controlled.
const (
	// EgressSandboxed: the process runs in its own network namespace; the
	// egress proxy is its only way out.
	EgressSandboxed EgressMode = "sandboxed"
	// EgressNetworkPolicy: a Kubernetes NetworkPolicy lets the pod reach only
	// DNS and WeKnora, whose egress proxy applies the grant. Enforced when the
	// cluster's network plugin enforces NetworkPolicy.
	EgressNetworkPolicy EgressMode = "networkPolicy"
	// EgressProxy: the process is given the egress proxy, but code that
	// ignores the proxy variables can connect directly.
	EgressProxy EgressMode = "proxy"
	// EgressUnmanaged: the plugin runs on infrastructure WeKnora does not
	// control (a remote service).
	EgressUnmanaged EgressMode = "unmanaged"
)

// InstanceStatus reports one instance of a plugin, for the plugin center.
type InstanceStatus struct {
	// Node is the WeKnora node or plugin host running the instance.
	Node      string        `json:"node"`
	Version   string        `json:"version"`
	State     InstanceState `json:"state"`
	Error     string        `json:"error,omitempty"`
	UpdatedAt time.Time     `json:"updatedAt"`
	// Egress is how the instance's outbound traffic is controlled; empty for
	// plugins without code.
	Egress EgressMode `json:"egress,omitempty"`
}

// Driver runs plugins of one runtime type.
type Driver interface {
	// Type is the manifest runtime.type this driver serves.
	Type() manifest.RuntimeType
	// Ensure brings the plugin's current version to a runnable state. It is
	// idempotent: reconciling calls it again on every pass.
	Ensure(ctx context.Context, m *manifest.Manifest) error
	// Remove stops and cleans up one version of a plugin.
	Remove(ctx context.Context, pluginID, version string) error
	// Resolve picks the endpoint for one request, balancing across
	// instances. tenantID matters to drivers that isolate tenants.
	Resolve(ctx context.Context, pluginID string, tenantID uint64) (Endpoint, error)
	// Status lists the plugin's instances.
	Status(ctx context.Context, pluginID string) ([]InstanceStatus, error)
}

// Set holds one driver per runtime type.
type Set struct {
	mu      sync.RWMutex
	drivers map[manifest.RuntimeType]Driver
}

// NewSet returns a Set holding the given drivers.
func NewSet(drivers ...Driver) *Set {
	s := &Set{drivers: make(map[manifest.RuntimeType]Driver)}
	for _, d := range drivers {
		s.drivers[d.Type()] = d
	}
	return s
}

// For returns the driver for a runtime type.
func (s *Set) For(t manifest.RuntimeType) (Driver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.drivers[t]
	if !ok {
		return nil, fmt.Errorf("no driver for runtime %q in this deployment", t)
	}
	return d, nil
}
