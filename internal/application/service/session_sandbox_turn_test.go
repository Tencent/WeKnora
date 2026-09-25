package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestHoldSandboxTurnOpensAndClosesTheLease(t *testing.T) {
	holder := &turnLeaseManager{}
	svc := &sessionService{sandboxMgr: holder}

	release, err := svc.holdSandboxTurn(context.Background(), "session-a", "")
	require.NoError(t, err)
	require.Equal(t, 1, holder.begins)
	require.Zero(t, holder.ends)

	release()
	require.Equal(t, 1, holder.ends)
}

// A lease-store blip must not reject the user's message: only a rewind lock
// is fatal, everything else degrades to running the turn without a lease.
func TestHoldSandboxTurnDegradesWhenBeginFails(t *testing.T) {
	holder := &turnLeaseManager{beginErr: context.Canceled}
	svc := &sessionService{sandboxMgr: holder}

	release, err := svc.holdSandboxTurn(context.Background(), "session-a", "")
	require.NoError(t, err)
	require.Equal(t, 1, holder.begins)
	release()
	require.Zero(t, holder.ends)
}

func TestHoldSandboxTurnFailsWhenRewindLocked(t *testing.T) {
	holder := &turnLeaseManager{beginErr: sandbox.ErrSessionRewindLocked}
	svc := &sessionService{sandboxMgr: holder}

	release, err := svc.holdSandboxTurn(context.Background(), "session-a", "")
	require.ErrorIs(t, err, sandbox.ErrSessionRewindLocked)
	release()
	require.Zero(t, holder.ends)
}

func TestHoldSandboxTurnUsesBusyGateWithoutSandboxManager(t *testing.T) {
	gate := NewSessionBusyGate()
	svc := &sessionService{busyGate: gate}

	release, err := svc.holdSandboxTurn(context.Background(), "session-a", "")
	require.NoError(t, err)
	_, err = gate.TryLockRewind("session-a")
	require.ErrorIs(t, err, ErrRewindSourceBusy)

	release()
	unlock, err := gate.TryLockRewind("session-a")
	require.NoError(t, err)
	unlock()
}

func TestRejectSendIfRewindingWhenLocalGateHeld(t *testing.T) {
	gate := NewSessionBusyGate()
	svc := &sessionService{busyGate: gate}
	unlock, err := gate.TryLockRewind("session-a")
	require.NoError(t, err)

	require.ErrorIs(t, svc.RejectSendIfRewinding(context.Background(), "session-a"), sandbox.ErrSessionRewindLocked)
	unlock()
	require.NoError(t, svc.RejectSendIfRewinding(context.Background(), "session-a"))
}

func TestRejectSendIfRewindingWhenLockHeld(t *testing.T) {
	mgr := &rewindLockManager{held: true}
	svc := &sessionService{sandboxMgr: mgr}
	require.ErrorIs(t, svc.RejectSendIfRewinding(context.Background(), "session-a"), sandbox.ErrSessionRewindLocked)

	mgr.held = false
	require.NoError(t, svc.RejectSendIfRewinding(context.Background(), "session-a"))
}

type turnLeaseManager struct {
	stagingSandboxManager
	begins   int
	ends     int
	beginErr error
	endErr   error
}

func (m *turnLeaseManager) BeginSessionTurn(context.Context, string) error {
	m.begins++
	return m.beginErr
}

func (m *turnLeaseManager) EndSessionTurn(context.Context, string) error {
	m.ends++
	return m.endErr
}

type rewindLockManager struct {
	stagingSandboxManager
	held bool
}

func (m *rewindLockManager) HasRewindLock(context.Context, string) (bool, error) {
	return m.held, nil
}
