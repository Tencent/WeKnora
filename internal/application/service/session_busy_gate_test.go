package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestSessionBusyGateSendBlocksRewind(t *testing.T) {
	gate := NewSessionBusyGate()

	release, err := gate.HoldSend("sess-1")
	require.NoError(t, err)

	_, err = gate.TryLockRewind("sess-1")
	require.ErrorIs(t, err, ErrRewindSourceBusy)

	release()
	unlock, err := gate.TryLockRewind("sess-1")
	require.NoError(t, err)
	unlock()
}

func TestSessionBusyGateRewindBlocksSend(t *testing.T) {
	gate := NewSessionBusyGate()

	unlock, err := gate.TryLockRewind("sess-1")
	require.NoError(t, err)
	require.True(t, gate.RewindHeld("sess-1"))

	_, err = gate.HoldSend("sess-1")
	require.ErrorIs(t, err, sandbox.ErrSessionRewindLocked)

	unlock()
	require.False(t, gate.RewindHeld("sess-1"))
	release, err := gate.HoldSend("sess-1")
	require.NoError(t, err)
	release()
}

func TestSessionBusyGateNestedSendHolds(t *testing.T) {
	gate := NewSessionBusyGate()
	first, err := gate.HoldSend("sess-1")
	require.NoError(t, err)
	second, err := gate.HoldSend("sess-1")
	require.NoError(t, err)

	_, err = gate.TryLockRewind("sess-1")
	require.ErrorIs(t, err, ErrRewindSourceBusy)

	first()
	_, err = gate.TryLockRewind("sess-1")
	require.ErrorIs(t, err, ErrRewindSourceBusy)

	second()
	unlock, err := gate.TryLockRewind("sess-1")
	require.NoError(t, err)
	unlock()
}
