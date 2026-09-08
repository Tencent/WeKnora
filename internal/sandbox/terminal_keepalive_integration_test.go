//go:build sandbox_terminal_integration

package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// This observer forwards every request to the real guarded transport. It
// counts actual TTL refreshes, not simulated sandbox responses.
type terminalConnectObserver struct {
	next     http.RoundTripper
	connects atomic.Int32
}

func (o *terminalConnectObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/connect") {
		o.connects.Add(1)
	}
	return o.next.RoundTrip(req)
}

func TestTerminalRealE2BKeepalive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Type, cfg.AllowPrivateEndpoints = SandboxTypeE2B, true
	cfg.E2BAPIURL, cfg.E2BProxyURL = "http://127.0.0.1:18080/e2b/v1", "http://127.0.0.1:18080"
	cfg.E2BSandboxDomain, cfg.E2BTemplate = "localhost", "weknora-wb-python"
	cfg.E2BAPIKey = "weknora-wb-local-api-token-00000001"
	cfg.E2BSandboxTTL = 3 * time.Second
	policy := OutboundURLPolicy{AllowPrivate: true}
	observer := &terminalConnectObserver{next: NewGuardedTransportWithPolicy(policy)}
	client, err := NewE2BRemoteClientWithPool(cfg, NewSandboxGatewayTransportPoolWithPolicy(observer, policy))
	require.NoError(t, err)
	mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{Config: cfg, Client: client,
		Store: NewMemorySessionSandboxBindingStore(), Checker: PermissiveSessionExistenceChecker{}, SkipHealthProbe: true})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(types.WithSandboxTenantID(context.Background(), 918272), 30*time.Second)
	defer cancel()
	session := fmt.Sprintf("weknora-wb-terminal-keepalive-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(types.WithSandboxTenantID(context.Background(), 918272), 30*time.Second)
		defer cancel()
		require.NoError(t, mgr.DestroySession(ctx, session))
	})
	term, err := mgr.OpenSessionTerminal(ctx, session, TerminalRequest{Command: "printf READY; sleep 5; printf DONE", Timeout: 10 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = term.Close() })
	terminalReadUntil(t, term, "READY")
	before := observer.connects.Load()
	terminalReadUntil(t, term, "DONE")
	exit, err := term.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, exit.ExitCode)
	require.GreaterOrEqual(t, observer.connects.Load()-before, int32(3), "active terminal must renew the real provider TTL")
}
