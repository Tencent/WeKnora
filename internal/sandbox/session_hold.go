package sandbox

import (
	"context"
	"errors"
	"fmt"
)

// ErrHeldSandboxLost reports that the sandbox an operation holds was reclaimed
// or replaced. A fresh sandbox boots the previous image and lacks everything the
// operation already did inside the held one, so continuing there is never safe.
var ErrHeldSandboxLost = errors.New("sandbox: the held sandbox is gone or was replaced")

var errBoundSandboxChanged = errors.New("sandbox: bound sandbox changed before maintenance command")

type heldSandboxKey struct{}

type heldSandbox struct {
	sessionID string
	sandboxID string
}

func heldSandboxID(ctx context.Context, sessionID string) string {
	held, ok := ctx.Value(heldSandboxKey{}).(heldSandbox)
	if !ok || held.sessionID != sessionID {
		return ""
	}
	return held.sandboxID
}

// HoldSessionSandbox pins the session's live sandbox for an operation that
// outlives a single command. Every call made with the returned ctx reaches that
// sandbox or fails with ErrHeldSandboxLost; none provisions, rebuilds or
// recovers another. Until release runs, the provider's idle TTL is refreshed:
// it is reset only when a command starts, and one long command can outlast it.
func (m *SessionBoundManager) HoldSessionSandbox(
	ctx context.Context, sessionID string,
) (context.Context, func(), error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, nil, err
	}
	handle, found, err := m.lookupSessionHandle(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, fmt.Errorf("sandbox: no live sandbox for session %s", sessionID)
	}
	held := context.WithValue(ctx, heldSandboxKey{}, heldSandbox{sessionID: sessionID, sandboxID: handle.ID()})
	refreshCtx, release := context.WithCancel(ctx)
	if refresher, ok := DesktopTTLRefresherFrom(m.client); ok {
		refresher.StartDesktopTTLRefresh(refreshCtx, handle)
	}
	return held, release, nil
}

func (m *SessionBoundManager) connectHeldSandbox(
	ctx context.Context, key SessionSandboxKey, sandboxID string,
) (RemoteSandboxHandle, error) {
	handle, err := m.connectExpectedSandbox(ctx, key, sandboxID)
	if errors.Is(err, errBoundSandboxChanged) {
		return nil, fmt.Errorf("%w (session %s, sandbox %s)", ErrHeldSandboxLost, key.SessionID, sandboxID)
	}
	return handle, err
}

var _ SessionSandboxHolder = (*SessionBoundManager)(nil)
