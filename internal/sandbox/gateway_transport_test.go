package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingRoundTripper records how many requests rode the control transport.
type countingRoundTripper struct {
	next  http.RoundTripper
	mu    sync.Mutex
	hosts []string
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.hosts = append(c.hosts, req.URL.Host)
	c.mu.Unlock()
	return c.next.RoundTrip(req)
}

func (c *countingRoundTripper) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.hosts...)
}

// The data plane addresses sandboxes as "49983-{id}.{domain}" but must dial
// the configured proxy. Sharing one transport across both planes drops that
// rewrite, which is exactly the regression this guards: the proxy has to see
// the request, and it has to see the sandbox authority in the Host header.
func TestSandboxGatewayTransportPoolRoutesDataPlaneThroughProxy(t *testing.T) {
	api := newCubeMockServer(t)

	var mu sync.Mutex
	var proxyHosts []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxyHosts = append(proxyHosts, r.Host)
		mu.Unlock()
		api.handle(w, r)
	}))
	t.Cleanup(proxy.Close)

	cfg := testConfig(t, api)
	cfg.AllowPrivateEndpoints = true
	cfg.CubeProxyURL = proxy.URL

	policy := OutboundURLPolicy{AllowPrivate: true}
	control := &countingRoundTripper{next: NewGuardedTransportWithPolicy(policy)}
	client, err := NewCubeRemoteClientWithPool(cfg, NewSandboxGatewayTransportPoolWithPolicy(control, policy))
	require.NoError(t, err)

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

	api.SetExecutor(func(string, string, []string) (string, string, int) {
		return "ok", "", 0
	})
	result, err := client.Exec(ctx, handle, RemoteExecRequest{Command: "echo", Args: []string{"ok"}})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)

	mu.Lock()
	gotProxyHosts := append([]string(nil), proxyHosts...)
	mu.Unlock()
	require.NotEmpty(t, gotProxyHosts, "data plane never reached the proxy")
	require.Contains(t, gotProxyHosts[0], "49983-"+handle.ID()+".cube.app")

	// Control-plane calls ride the shared transport; data-plane calls do not.
	for _, host := range control.seen() {
		require.NotContains(t, host, "cube.app")
	}
	require.NotEmpty(t, control.seen())
}

// Configs pointing at the same proxy must share one pool - otherwise building
// a client per request pools nothing.
func TestSandboxGatewayTransportPoolReusesTransportPerProxyEndpoint(t *testing.T) {
	pool := NewSandboxGatewayTransportPoolWithPolicy(
		NewGuardedTransportWithPolicy(OutboundURLPolicy{AllowPrivate: true}),
		OutboundURLPolicy{AllowPrivate: true},
	)

	first := pool.RoundTripperFor(&Config{
		CubeProxyURL:      "http://127.0.0.1:8080",
		CubeSandboxDomain: "cube.app",
	}).(*gatewaySplitTransport)
	second := pool.RoundTripperFor(&Config{
		CubeProxyURL:      "http://127.0.0.1:8080",
		CubeSandboxDomain: "cube.app",
	}).(*gatewaySplitTransport)
	other := pool.RoundTripperFor(&Config{
		CubeProxyURL:      "http://127.0.0.1:9090",
		CubeSandboxDomain: "cube.app",
	}).(*gatewaySplitTransport)

	require.Same(t, first.data, second.data)
	require.NotSame(t, first.data, other.data)
	require.Same(t, first.control, other.control)
}

// Without a usable proxy URL the SDK dials the sandbox authority directly, so
// the split transport must not invent a data plane.
func TestSandboxGatewayTransportPoolWithoutProxyKeepsEverythingOnControl(t *testing.T) {
	pool := NewSandboxGatewayTransportPool(NewGuardedTransport())

	split := pool.RoundTripperFor(&Config{CubeSandboxDomain: "cube.app"}).(*gatewaySplitTransport)

	// A nil data transport is what sends sandbox authorities back to control.
	require.Nil(t, split.data)
}

// A self-hosted E2B-compatible control plane fronts every sandbox with one
// gateway, so the E2B provider must read its own gateway fields - and a plain
// HTTP gateway has to survive the SDK pinning the data-plane scheme to https.
func TestSandboxGatewayTransportPoolRoutesE2BDataPlane(t *testing.T) {
	pool := NewSandboxGatewayTransportPoolWithPolicy(
		NewGuardedTransportWithPolicy(OutboundURLPolicy{AllowPrivate: true}),
		OutboundURLPolicy{AllowPrivate: true},
	)

	split := pool.RoundTripperFor(&Config{
		Type:             SandboxTypeE2B,
		E2BProxyURL:      "http://127.0.0.1:18080",
		E2BSandboxDomain: "localhost",
		// Cube fields must be ignored for an E2B config.
		CubeProxyURL:      "http://127.0.0.1:9999",
		CubeSandboxDomain: "cube.app",
	}).(*gatewaySplitTransport)

	require.NotNil(t, split.data)
	require.Equal(t, "http", split.dataScheme)
	require.True(t, split.isDataPlane("49983-sbx.localhost"))
	require.False(t, split.isDataPlane("49983-sbx.cube.app"))
}

func TestGatewaySplitTransportAppliesGatewayScheme(t *testing.T) {
	recorder := &recordingRoundTripper{}
	split := &gatewaySplitTransport{
		control:       &recordingRoundTripper{},
		data:          recorder,
		sandboxDomain: "localhost",
		dataScheme:    "http",
	}

	request := httptest.NewRequest(http.MethodGet, "https://49983-sbx.localhost/files", nil)
	_, err := split.RoundTrip(request)
	require.NoError(t, err)

	require.Equal(t, "http", recorder.request.URL.Scheme)
	// The sandbox authority is what the gateway routes on; it must survive.
	require.Equal(t, "49983-sbx.localhost", recorder.request.URL.Host)
	require.Equal(t, "https", request.URL.Scheme, "the caller's request must not be mutated")
}

func TestGatewaySplitTransportClassifiesAuthorities(t *testing.T) {
	split := &gatewaySplitTransport{
		control:       NewGuardedTransport(),
		data:          NewGuardedTransport(),
		sandboxDomain: "cube.app",
	}

	require.True(t, split.isDataPlane("49983-abc.cube.app"))
	require.True(t, split.isDataPlane("49983-ABC.Cube.App"))
	require.False(t, split.isDataPlane("api.example.com"))
	// A lookalike suffix must not be mistaken for the sandbox domain.
	require.False(t, split.isDataPlane("evilcube.app"))
}
