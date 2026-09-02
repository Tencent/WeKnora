package sandbox

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxIDFromDataPlaneHost(t *testing.T) {
	// envd is addressed as "<port>-<sandboxID>.<domain>".
	require.Equal(t, "sb-123", SandboxIDFromDataPlaneHost("49983-sb-123.cube.app"))
	require.Equal(t, "i9x8", SandboxIDFromDataPlaneHost("80-i9x8.e2b.app"))
	require.Equal(t, "sb-1", SandboxIDFromDataPlaneHost("49983-sb-1.gateway.internal:8443"))
	// Control-plane and malformed authorities carry no sandbox.
	require.Empty(t, SandboxIDFromDataPlaneHost("api.e2b.app"))
	require.Empty(t, SandboxIDFromDataPlaneHost("notaport-sb-1.cube.app"))
	require.Empty(t, SandboxIDFromDataPlaneHost("49983-.cube.app"))
	require.Empty(t, SandboxIDFromDataPlaneHost(""))
}

func TestInboundTokenRegistryLifecycle(t *testing.T) {
	registry := NewInboundTokenRegistry()
	require.Empty(t, registry.Get("sb-1"))

	registry.Put("sb-1", "token-1")
	require.Equal(t, "token-1", registry.Get("sb-1"))

	// An empty token means "inbound is open"; storing it would be noise.
	registry.Put("sb-1", "")
	require.Equal(t, "token-1", registry.Get("sb-1"))

	registry.Delete("sb-1")
	require.Empty(t, registry.Get("sb-1"))
}

func TestInboundTokenRegistryNormalizesSandboxIDCase(t *testing.T) {
	registry := NewInboundTokenRegistry()
	registry.Put("Sb-MiXeD", "token-1")

	require.Equal(t, "token-1", registry.Get("Sb-MiXeD"))
	sandboxID := SandboxIDFromDataPlaneHost("49983-SB-MIXED.e2b.app")
	require.Equal(t, "sb-mixed", sandboxID)
	require.Equal(t, "token-1", registry.Get(sandboxID))

	registry.Delete("SB-MIXED")
	require.Empty(t, registry.Get("sb-mixed"))
}

func TestGatewayTransportInjectsInboundToken(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("e2b-traffic-access-token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pool := NewSandboxGatewayTransportPoolWithPolicy(
		server.Client().Transport,
		OutboundURLPolicy{AllowPrivate: true},
	)
	pool.InboundTokens().Put("sb-1", "token-1")
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BProxyURL = server.URL
	cfg.E2BSandboxDomain = "e2b.app"
	client := &http.Client{Transport: pool.RoundTripperFor(cfg)}

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	req.Host = "49983-sb-1.e2b.app"
	req.URL.Host = "49983-sb-1.e2b.app"
	_, err = client.Transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, "token-1", gotToken,
		"go-e2b never sends this header, so the transport has to")
}

func TestGatewayTransportLeavesUnknownSandboxAlone(t *testing.T) {
	pool := NewSandboxGatewayTransportPool(nil)
	recorder := &roundTripRecorder{}
	transport := &gatewaySplitTransport{
		control:      recorder,
		inboundToken: pool.InboundTokens(),
	}

	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-unknown.e2b.app/", nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.Empty(t, recorder.lastHeader)
	require.False(t, recorder.lastHeaderPresent)
}

func TestGatewayTransportInjectsInboundTokenOnControlPath(t *testing.T) {
	recorder := &roundTripRecorder{}
	pool := NewSandboxGatewayTransportPool(recorder)
	pool.InboundTokens().Put("sb-1", "token-1")
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BSandboxDomain = "e2b.app"
	transport := pool.RoundTripperFor(cfg)

	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-1.e2b.app/", nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, "token-1", recorder.lastHeader)
}

// Cube's SDK sets the header itself; the transport must not clobber it.
func TestGatewayTransportDoesNotOverwriteExistingToken(t *testing.T) {
	pool := NewSandboxGatewayTransportPool(nil)
	pool.InboundTokens().Put("sb-1", "registry-token")
	recorder := &roundTripRecorder{}
	transport := &gatewaySplitTransport{
		control:      recorder,
		inboundToken: pool.InboundTokens(),
	}

	req, err := http.NewRequest(http.MethodGet, "https://49983-sb-1.cube.app/", nil)
	require.NoError(t, err)
	req.Header.Set("e2b-traffic-access-token", "sdk-token")
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, "sdk-token", recorder.lastHeader)
}

// roundTripRecorder stands in for the control transport and remembers what
// header the request carried by the time it got there.
type roundTripRecorder struct {
	lastHeader        string
	lastHeaderPresent bool
}

func (r *roundTripRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.lastHeader = req.Header.Get(InboundTokenHeader)
	_, r.lastHeaderPresent = req.Header[http.CanonicalHeaderKey(InboundTokenHeader)]
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    req,
	}, nil
}
