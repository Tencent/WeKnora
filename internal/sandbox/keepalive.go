package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RemoteSandboxKeepAliver refreshes a sandbox's provider idle timer.
// Background work runs outside chat turns, so nothing else tells the provider
// the sandbox is still in use. Cube and E2B move their timeout to ttl from
// now; Docker runs a no-op exec, which refreshes the activity marker its idle
// sweeper reads.
type RemoteSandboxKeepAliver interface {
	KeepAlive(ctx context.Context, handle RemoteSandboxHandle, ttl time.Duration) error
}

// keepAliverFrom narrows client to keep-alive. It looks through the Langfuse
// decorator rather than having the decorator forward: every client
// NewSessionBoundManager holds is wrapped, and a forwarding method would make
// a client without the capability look like it has one.
func keepAliverFrom(client RemoteSandboxClient) (RemoteSandboxKeepAliver, bool) {
	switch c := client.(type) {
	case nil:
		return nil, false
	case *langfuseRemoteClient:
		return keepAliverFrom(c.inner)
	case *langfuseSnapshotClient:
		return keepAliverFrom(c.inner)
	}
	keeper, ok := client.(RemoteSandboxKeepAliver)
	return keeper, ok
}

var (
	_ RemoteSandboxKeepAliver = (*CubeRemoteClient)(nil)
	_ RemoteSandboxKeepAliver = (*E2BRemoteClient)(nil)
	_ RemoteSandboxKeepAliver = (*DockerRemoteClient)(nil)
)

// keepAliveRefreshDivisor sets how often a caller refreshes: every TTL/3
// (shared design §5), so two missed refreshes still land before expiry.
const keepAliveRefreshDivisor = 3

// KeepAliveSession refreshes the provider idle timer of the sandbox bound to
// sessionID and returns how long the caller may wait before the next refresh.
// It is lookup-only: it never provisions, never rebuilds and never connects to
// anything but the bound sandbox, so a session with nothing bound, or a
// provider with nothing to refresh, returns (0, nil).
func (m *SessionBoundManager) KeepAliveSession(ctx context.Context, sessionID string) (time.Duration, error) {
	return m.keepAliveSession(ctx, sessionID, "")
}

// KeepAliveSessionExpected is KeepAliveSession for callers that selected a
// specific sandbox. Lookup is only against expectedSandboxID: a missing
// binding or a different sandbox returns (0, nil) and never allocates.
func (m *SessionBoundManager) KeepAliveSessionExpected(
	ctx context.Context, sessionID, expectedSandboxID string,
) (time.Duration, error) {
	return m.keepAliveSession(ctx, sessionID, expectedSandboxID)
}

func (m *SessionBoundManager) keepAliveSession(
	ctx context.Context, sessionID, expectedSandboxID string,
) (time.Duration, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return 0, err
	}
	keeper, ok := keepAliverFrom(m.client)
	if !ok {
		return 0, nil
	}
	req, err := buildSessionCreateRequest(m.client.Provider(), m.config)
	if err != nil {
		return 0, err
	}
	ttl := req.Timeout.Value
	if ttl <= 0 {
		return 0, nil
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	handle, found, err := m.lookupSessionHandleForKey(ctx, sc.machine(), expectedSandboxID)
	if err != nil {
		// A mismatched expected id is "not this sandbox", not a failure to refresh
		// and not a reason to allocate a replacement.
		if expectedSandboxID != "" && strings.Contains(err.Error(), "bound sandbox changed") {
			return 0, nil
		}
		return 0, err
	}
	if !found {
		return 0, nil
	}
	if err := keeper.KeepAlive(ctx, handle, ttl); err != nil {
		return 0, fmt.Errorf("sandbox: keep session sandbox alive: %w", err)
	}
	return ttl / keepAliveRefreshDivisor, nil
}
