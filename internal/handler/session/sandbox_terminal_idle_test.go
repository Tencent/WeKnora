package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

func TestTerminalIdleExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	last := now.Add(-15 * time.Minute)

	require.False(t, terminalIdleExpired(time.Time{}, 15*time.Minute, now),
		"no activity timestamp must not expire")
	require.False(t, terminalIdleExpired(last, 0, now),
		"zero idle must disable the watcher")
	require.False(t, terminalIdleExpired(last, 20*time.Minute, now))
	require.True(t, terminalIdleExpired(last, 15*time.Minute, now))
	require.True(t, terminalIdleExpired(last, 10*time.Minute, now))
}

// The provision flag authorises creating a microVM from a GET, so it must be
// opt-in: absent, empty, or malformed all have to read as "do not create".
func TestTerminalFlagParam(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"1", "true", "TRUE", " yes ", "Yes"} {
		require.True(t, terminalFlagParam(raw), "expected %q to opt in", raw)
	}
	for _, raw := range []string{"", " ", "0", "false", "no", "2", "on", "provision", "1;"} {
		require.False(t, terminalFlagParam(raw), "expected %q to stay opted out", raw)
	}
}

func TestTerminalTenantParam(t *testing.T) {
	t.Parallel()
	require.Equal(t, uint64(0), terminalTenantParam(""))
	require.Equal(t, uint64(0), terminalTenantParam("0"))
	require.Equal(t, uint64(0), terminalTenantParam("-1"))
	require.Equal(t, uint64(84), terminalTenantParam("84"))
	require.Equal(t, uint64(84), terminalTenantParam(" 84 "))
}

func TestTerminalAuthRecheckInterval(t *testing.T) {
	t.Parallel()
	require.GreaterOrEqual(t, terminalAuthRecheckInterval, time.Minute)
	require.LessOrEqual(t, terminalAuthRecheckInterval, 2*time.Minute)
	require.Equal(t, "AUTH_REVOKED", terminalErrAuth)
}

func TestTerminalErrorFrame(t *testing.T) {
	t.Parallel()

	code, _ := terminalErrorFrame(sandbox.ErrNoLiveSessionSandbox)
	require.Equal(t, terminalErrNotBound, code)

	code, _ = terminalErrorFrame(sandbox.ErrSandboxPaused)
	require.Equal(t, terminalErrPaused, code)

	code, _ = terminalErrorFrame(service.ErrTerminalUnsupported)
	require.Equal(t, terminalErrUnsupported, code)
}
