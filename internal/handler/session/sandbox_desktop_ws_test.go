package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

func TestDesktopRelayIdleFiresWithoutOpcodeActivity(t *testing.T) {
	// The control this whole design exists for: a desktop nobody is touching
	// must go idle even though bytes keep flowing (XFCE clock, websockify
	// heartbeat, ContinuousUpdates).
	relay := &desktopRelay{sessionID: "sess-1", idleDisconnect: 50 * time.Millisecond}
	relay.touch()

	// Feed traffic that is NOT activity: handshake, then repeated
	// FramebufferUpdateRequest.
	relay.stream.Consume(append([]byte("RFB 003.008\n"), 1, 1))
	for i := 0; i < 20; i++ {
		require.False(t, relay.stream.Consume(append([]byte{3}, make([]byte, 9)...)))
	}

	time.Sleep(80 * time.Millisecond)
	require.True(t, relay.idleFor(time.Now()) >= 50*time.Millisecond)
	require.True(t, relay.isIdle(time.Now()))
}

func TestDesktopRelayNotIdleAfterPointerEvent(t *testing.T) {
	relay := &desktopRelay{sessionID: "sess-1", idleDisconnect: time.Minute}
	relay.stream.Consume(append([]byte("RFB 003.008\n"), 1, 1))
	if relay.stream.Consume(append([]byte{5}, make([]byte, 5)...)) {
		relay.touch()
	}
	require.False(t, relay.isIdle(time.Now()))
}

func TestDesktopRelayFrontendReportKeepsAliveAfterParserFallback(t *testing.T) {
	// After an unknown opcode the parser is off for good, so the frontend POST
	// is the only remaining activity source.
	relay := &desktopRelay{sessionID: "sess-1", idleDisconnect: time.Minute}
	relay.stream.Consume(append([]byte("RFB 003.008\n"), 1, 1))
	relay.stream.Consume([]byte{99, 0, 0})
	require.False(t, relay.stream.ParsingEnabled())

	relay.lastActivity = time.Now().Add(-2 * time.Minute)
	require.True(t, relay.isIdle(time.Now()))
	relay.TouchFromFrontend()
	require.False(t, relay.isIdle(time.Now()))
}

func TestDesktopRelayFrontendReportIsIgnoredWhileParserHealthy(t *testing.T) {
	relay := &desktopRelay{sessionID: "sess-1", idleDisconnect: time.Minute}
	relay.stream.Consume(append([]byte("RFB 003.008\n"), 1, 1))
	require.True(t, relay.stream.ParsingEnabled())

	relay.lastActivity = time.Now().Add(-2 * time.Minute)
	require.True(t, relay.isIdle(time.Now()))
	relay.TouchFromFrontend()
	require.True(t, relay.isIdle(time.Now()),
		"a healthy parser must ignore the activity POST so a client cannot keep the TTL by posting")
}

func TestDesktopSandboxRebuiltUsesStore(t *testing.T) {
	h := &Handler{desktopLast: service.NewSandboxDesktopLastStore(nil)}
	ctx := context.Background()
	require.False(t, h.desktopSandboxRebuilt(ctx, "sess-1", "sbx-a"))
	require.True(t, h.desktopSandboxRebuilt(ctx, "sess-1", "sbx-b"))
	require.False(t, h.desktopSandboxRebuilt(ctx, "sess-1", "sbx-b"))
}

func TestDesktopLimiterSessionKeyTakesOver(t *testing.T) {
	// A reconnect must not be rejected by the session's own previous relay:
	// after an unclean disconnect the old relay can hold the slot for up to
	// desktopReadTimeout before it notices, and every retry used to come
	// back DESKTOP_BUSY. Latest wins: the second holder evicts the first,
	// whose hold context ends so its relay can close its browser hop.
	first, firstHold := sessionDesktopLimiter.acquireSession("sess-take")
	require.NotNil(t, firstHold)

	second, secondHold := sessionDesktopLimiter.acquireSession("sess-take")
	require.NotNil(t, secondHold)
	select {
	case <-firstHold.Done():
	case <-time.After(time.Second):
		t.Fatal("takeover must end the previous holder's hold so its relay closes")
	}

	// The evicted holder's release must not drop the new holder's entry.
	first()
	select {
	case <-secondHold.Done():
		t.Fatal("the new holder's slot must survive the old relay's release")
	default:
	}
	second()
}

func TestDesktopLimiterMachineKeyStaysExclusive(t *testing.T) {
	first, ok := sessionDesktopLimiter.acquireExclusive("machine-take")
	require.True(t, ok)
	_, ok = sessionDesktopLimiter.acquireExclusive("machine-take")
	require.False(t, ok, "a machine slot guards another session's shared screen; it must never be taken over")
	first()
	next, ok := sessionDesktopLimiter.acquireExclusive("machine-take")
	require.True(t, ok, "releasing must free the slot")
	next()
}

func TestDesktopSlotIsExclusivePerMachineAcrossSessions(t *testing.T) {
	h, _, _ := newDesktopSlotHandler(t)
	ctx := context.Background()
	key := desktopMachineSlotKey("machine-1")
	release, got, _, err := h.acquireDesktopSlot(ctx, key)
	require.NoError(t, err)
	require.True(t, got)
	_, got, _, err = h.acquireDesktopSlot(ctx, key)
	require.NoError(t, err)
	require.False(t, got, "a second session on the same machine gets DESKTOP_BUSY")
	release()
	_, got, _, err = h.acquireDesktopSlot(ctx, desktopMachineSlotKey("machine-2"))
	require.NoError(t, err)
	require.True(t, got)
}

func TestSeatDesktopsDoNotTakeTheMachineSlot(t *testing.T) {
	h, _, client := newDesktopSlotHandler(t)
	ctx := context.Background()
	releaseA, holdA, busyA, err := h.acquireSharedDesktopSlot(ctx, "machine-1", "1")
	require.NoError(t, err)
	require.False(t, busyA)
	require.Nil(t, holdA)
	releaseB, _, busyB, err := h.acquireSharedDesktopSlot(ctx, "machine-1", "7")
	require.NoError(t, err)
	require.False(t, busyB)
	require.Equal(t, int64(0), client.Exists(ctx, desktopMachineSlotKey("machine-1")).Val())
	releaseA()
	releaseB()
}

func TestSharedScreenStillTakesTheMachineSlot(t *testing.T) {
	h, _, _ := newDesktopSlotHandler(t)
	ctx := context.Background()
	for _, token := range []string{"", "0"} {
		release, hold, busy, err := h.acquireSharedDesktopSlot(ctx, "machine-1", token)
		require.NoError(t, err, token)
		require.False(t, busy, token)
		require.NotNil(t, hold, token)
		_, _, busy, err = h.acquireSharedDesktopSlot(ctx, "machine-1", token)
		require.NoError(t, err, token)
		require.True(t, busy, "token %q still shares :0", token)
		release()
	}
}

func TestSeatRelayWatchAllowsAMissingMachineHold(t *testing.T) {
	session, cancel := context.WithCancel(context.Background())
	merged, stop := mergeDesktopHolds(session, nil, context.Background())
	require.NotNil(t, merged)
	cancel()
	require.Eventually(t, func() bool { return merged.Err() != nil }, time.Second, 5*time.Millisecond)
	stop()
}

func TestMergeDesktopHoldsEndsWithAnyHold(t *testing.T) {
	a, cancelA := context.WithCancel(context.Background())
	b, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	merged, stopMerged := mergeDesktopHolds(a, nil, b)
	defer stopMerged()
	cancelA()
	require.Eventually(t, func() bool { return merged.Err() != nil }, time.Second, 5*time.Millisecond)
	none, stopNone := mergeDesktopHolds(nil, nil)
	require.Nil(t, none)
	stopNone()
}

func TestDesktopSlotReleaseIsIdempotent(t *testing.T) {
	release, hold := sessionDesktopLimiter.acquireSession("sess-idem")
	release()
	release()
	select {
	case <-hold.Done():
	default:
		t.Fatal("release must end the hold so the relay's slot watcher stops")
	}
	// A double release must not corrupt the map: the key is free again.
	next, ok := sessionDesktopLimiter.acquireExclusive("sess-idem")
	require.True(t, ok)
	next()
}

func withDesktopSlotTiming(t *testing.T, lease, renew time.Duration) {
	t.Helper()
	origLease, origRenew := desktopSlotLease, desktopSlotRenew
	desktopSlotLease, desktopSlotRenew = lease, renew
	t.Cleanup(func() {
		desktopSlotLease, desktopSlotRenew = origLease, origRenew
	})
}

// withDesktopSessionSlotTiming shortens the session slot's lease and
// renewal; a takeover evicts the previous holder only at its next renewal
// tick, so tests make that tick fast.
func withDesktopSessionSlotTiming(t *testing.T, lease, renew time.Duration) {
	t.Helper()
	origLease, origRenew := desktopSlotLease, desktopSessionSlotRenew
	desktopSlotLease, desktopSessionSlotRenew = lease, renew
	t.Cleanup(func() {
		desktopSlotLease, desktopSessionSlotRenew = origLease, origRenew
	})
}

func newDesktopSlotHandler(t *testing.T) (*Handler, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return &Handler{redis: client}, mini, client
}

func TestDesktopSlotRenewSurvivesTransientRedisError(t *testing.T) {
	withDesktopSessionSlotTiming(t, 400*time.Millisecond, 50*time.Millisecond)
	h, mini, client := newDesktopSlotHandler(t)
	ctx := context.Background()

	release, hold, err := h.acquireSessionDesktopSlot(ctx, "sess-blip")
	require.NoError(t, err)
	require.NotNil(t, hold)
	defer release()

	owned, err := client.Get(ctx, desktopSlotKey("sess-blip")).Result()
	require.NoError(t, err)

	mini.SetError("timeout")
	time.Sleep(70 * time.Millisecond)
	mini.SetError("")
	for i := 0; i < 6; i++ {
		time.Sleep(70 * time.Millisecond)
		mini.FastForward(80 * time.Millisecond)
	}

	select {
	case <-hold.Done():
		t.Fatal("a redis blip must not drop the desktop slot")
	default:
	}
	still, err := client.Get(ctx, desktopSlotKey("sess-blip")).Result()
	require.NoError(t, err)
	require.Equal(t, owned, still, "the original relay must still own the slot after a blip")
}

func TestDesktopSlotCancelsHoldWhenRedisStaysDownPastLease(t *testing.T) {
	withDesktopSessionSlotTiming(t, 200*time.Millisecond, 40*time.Millisecond)
	h, mini, _ := newDesktopSlotHandler(t)
	ctx := context.Background()

	release, hold, err := h.acquireSessionDesktopSlot(ctx, "sess-down")
	require.NoError(t, err)
	require.NotNil(t, hold)
	defer release()

	mini.SetError("timeout")
	select {
	case <-hold.Done():
	case <-time.After(time.Second):
		t.Fatal("redis down for a full lease must drop the hold; " +
			"otherwise a recovered replica would still claim exclusivity")
	}
}

func TestDesktopSlotCancelsHoldWhenOwnershipIsStolen(t *testing.T) {
	withDesktopSlotTiming(t, 400*time.Millisecond, 40*time.Millisecond)
	h, _, client := newDesktopSlotHandler(t)
	ctx := context.Background()
	key := desktopMachineSlotKey("machine-stolen")

	release, got, hold, err := h.acquireDesktopSlot(ctx, key)
	require.NoError(t, err)
	require.True(t, got)
	defer release()

	require.NoError(t, client.Set(ctx, key, "intruder", time.Minute).Err())
	select {
	case <-hold.Done():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("losing the redis key must cancel the hold so the relay can drop")
	}

	_, got, _, err = h.acquireDesktopSlot(ctx, key)
	require.NoError(t, err)
	require.False(t, got, "the thief still owns the key; a machine slot must never be overwritten")
}

func TestSessionDesktopSlotTakeoverReplacesTheHolder(t *testing.T) {
	// The reconnect-after-disconnect bug: the session slot was SETNX, so a
	// previous relay that had not yet noticed its dead browser hop answered
	// every reconnect with DESKTOP_BUSY. The takeover must replace the
	// holder, end its hold (so its relay closes), and survive the loser's
	// belated release.
	withDesktopSessionSlotTiming(t, 400*time.Millisecond, 20*time.Millisecond)
	h, _, client := newDesktopSlotHandler(t)
	ctx := context.Background()

	first, firstHold, err := h.acquireSessionDesktopSlot(ctx, "sess-relay")
	require.NoError(t, err)

	second, secondHold, err := h.acquireSessionDesktopSlot(ctx, "sess-relay")
	require.NoError(t, err, "a reconnect must take over the slot instead of DESKTOP_BUSY")
	defer second()

	select {
	case <-firstHold.Done():
	case <-time.After(time.Second):
		t.Fatal("the evicted relay's hold must end so its browser hop closes")
	}

	owned, err := client.Get(ctx, desktopSlotKey("sess-relay")).Result()
	require.NoError(t, err)
	require.NotEmpty(t, owned)

	// The evicted holder's release compares tokens, so it cannot delete the
	// successor's key.
	first()
	still, err := client.Get(ctx, desktopSlotKey("sess-relay")).Result()
	require.NoError(t, err)
	require.Equal(t, owned, still, "the old relay's release must not drop the new holder's slot")

	select {
	case <-secondHold.Done():
		t.Fatal("the new holder's hold must outlive the old relay's release")
	default:
	}
}

func TestDesktopSlotCancelsHoldWhenKeyExpires(t *testing.T) {
	withDesktopSessionSlotTiming(t, 400*time.Millisecond, 40*time.Millisecond)
	h, _, client := newDesktopSlotHandler(t)
	ctx := context.Background()

	release, hold, err := h.acquireSessionDesktopSlot(ctx, "sess-expired")
	require.NoError(t, err)
	require.NotNil(t, hold)
	defer release()

	require.NoError(t, client.Del(ctx, desktopSlotKey("sess-expired")).Err())
	select {
	case <-hold.Done():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("an expired slot must cancel the hold; otherwise a second client can join")
	}

	second, secondHold, err := h.acquireSessionDesktopSlot(ctx, "sess-expired")
	require.NoError(t, err)
	defer second()
	_ = secondHold
}

func TestDesktopIdleDisconnectFollowsWorkspaceTerminalSetting(t *testing.T) {
	require.Equal(t, time.Hour, desktopIdleDisconnect(&service.SessionDesktop{
		IdleDisconnect: time.Hour,
	}))
	require.Equal(t, sandbox.DefaultTerminalIdleDisconnect, desktopIdleDisconnect(&service.SessionDesktop{}))
	require.Equal(t, sandbox.DefaultTerminalIdleDisconnect, desktopIdleDisconnect(nil))
}

func TestDesktopHandshakeTimeoutIsShorterThanRelayReadTimeout(t *testing.T) {
	// Reusing the 120s relay read deadline for the pre-upgrade RFB hello held
	// the Redis slot for two minutes; retries became DESKTOP_BUSY. The hello
	// is 12 bytes as soon as x11vnc accepts, so a short bound is enough.
	require.Greater(t, desktopReadTimeout, desktopHandshakeTimeout)
	require.LessOrEqual(t, desktopHandshakeTimeout, 15*time.Second)
}

func TestDesktopErrorStatusMapsLookupFailures(t *testing.T) {
	status, code := desktopErrorStatus(sandbox.ErrNoLiveSessionSandbox)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, desktopErrNotBound, code)

	status, code = desktopErrorStatus(sandbox.ErrSandboxPaused)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, desktopErrPaused, code)

	status, code = desktopErrorStatus(service.ErrDesktopUnsupported)
	require.Equal(t, http.StatusNotImplemented, status)
	require.Equal(t, desktopErrUnsupported, code)

	status, code = desktopErrorStatus(service.ErrDesktopStartFailed)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, desktopErrStartFailed, code)
}

func TestSandboxDesktopWSLookupIsOptInProvision(t *testing.T) {
	// Opening the panel is a GET. Creating or resuming a microVM must stay
	// behind an explicit provision=1, the same contract as the terminal tab.
	src, err := os.ReadFile("sandbox_desktop_ws.go")
	require.NoError(t, err)
	text := string(src)
	require.Contains(t, text, `allowProvision := terminalFlagParam(c.Query("provision"))`)
	// Locate runs before either slot exists, so the pin uses the request
	// context. Start is what ensureCtx (the slot hold) cancels.
	require.Contains(t, text, `h.terminalProvisionPin(ctx, c, allowProvision)`)
	require.NotContains(t, text, `terminalProvisionPin(ctx, c, true)`)
	// The JS WebSocket API never exposes a failed handshake's HTTP status.
	// Overlay states (not bound / paused) have to ride a close reason after
	// upgrade, or the panel shows a generic error instead of the confirm card.
	require.Contains(t, text, "writeDesktopProtocolClose")
}

func TestDesktopRelayTearsDownWhenAuthIsRevoked(t *testing.T) {
	prev := terminalAuthRecheckInterval
	terminalAuthRecheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { terminalAuthRecheckInterval = prev })

	browserClient, sandboxClient, runReturned, _ := startDesktopRelay(t, func(context.Context) error {
		return service.ErrTerminalAuthDenied
	})

	select {
	case <-runReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("run() still blocked after auth was revoked; the desktop would keep relaying")
	}

	require.NoError(t, browserClient.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err := browserClient.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, desktopErrAuth, closeErr.Text)

	require.NoError(t, sandboxClient.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err = sandboxClient.ReadMessage()
	require.Error(t, err, "sandbox hop must close with the browser when auth is revoked")
}

func TestDesktopRelaySurvivesTransientAuthLookupFailure(t *testing.T) {
	prev := terminalAuthRecheckInterval
	terminalAuthRecheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { terminalAuthRecheckInterval = prev })

	var checks atomic.Int32
	browserClient, _, runReturned, _ := startDesktopRelay(t, func(context.Context) error {
		checks.Add(1)
		return errors.New("db down")
	})

	require.Eventually(t, func() bool {
		return checks.Load() >= 3
	}, 2*time.Second, 10*time.Millisecond, "watcher must keep retrying")

	select {
	case <-runReturned:
		t.Fatal("a lookup failure must not tear the desktop down")
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, browserClient.SetReadDeadline(time.Now().Add(150*time.Millisecond)))
	_, _, err := browserClient.ReadMessage()
	require.Error(t, err)
	netErr, ok := err.(interface{ Timeout() bool })
	require.True(t, ok && netErr.Timeout(),
		"expected a read timeout (desktop still open), got %v", err)
}

func TestSandboxDesktopWSWiresLiveAuthRecheck(t *testing.T) {
	src, err := os.ReadFile("sandbox_desktop_ws.go")
	require.NoError(t, err)
	text := string(src)
	require.Contains(t, text, "authCheck:")
	require.Contains(t, text, "checkTerminalAuth(checkCtx, authClaims, false)")
	require.Contains(t, text, "go r.watchAuth(done)")
	require.Contains(t, text, "go r.watchContext(done)")
}

func TestSandboxDesktopWSStartsTTLRefreshOnRelayCtx(t *testing.T) {
	src, err := os.ReadFile("sandbox_desktop_ws.go")
	require.NoError(t, err)
	text := string(src)
	require.Contains(t, text, "desktop.StartTTLRefresh(relayCtx)")
}

func TestSandboxDesktopWSTakesOverTheSessionSlot(t *testing.T) {
	// The handler must acquire the session slot with takeover semantics and
	// keep the slot-loss watcher that closes the evicted relay's browser hop
	// with DESKTOP_BUSY.
	src, err := os.ReadFile("sandbox_desktop_ws.go")
	require.NoError(t, err)
	text := string(src)
	require.Contains(t, text, "h.acquireSessionDesktopSlot(ctx, sessionID)")
	require.Contains(t, text, "relay.closeWith(desktopErrBusy)")
}

func TestSandboxDesktopWSDoesNotLabelAbandonedRequestsBusy(t *testing.T) {
	// When the browser gave up mid-ensure (the 40s handshake abort), the
	// request context dies and the old code answered 409 DESKTOP_BUSY from
	// the preHold branch — a lie that hid the real failure (a seat flock
	// wedged by a leaked dbus fd). An abandoned request must not be
	// reported as contention.
	src, err := os.ReadFile("sandbox_desktop_ws.go")
	require.NoError(t, err)
	text := string(src)
	abandoned := strings.Index(text, "the response goes nowhere")
	require.GreaterOrEqual(t, abandoned, 0,
		"the Start failure branch must check the request context before blaming the slot")
	startBranch := strings.Index(text, "h.desktopService.StartSessionDesktop(ensureCtx, target)")
	require.GreaterOrEqual(t, startBranch, 0)
	preHoldBranch := strings.Index(text, "preHold.Err() != nil")
	require.GreaterOrEqual(t, preHoldBranch, 0)
	require.Greater(t, preHoldBranch, abandoned,
		"the context check must precede the preHold DESKTOP_BUSY mapping")
}

func TestSandboxDesktopWSDoesNotRefreshProviderTTL(t *testing.T) {
	// The relay must not run its own SetTimeout loop. Cube/E2B refresh is
	// started on relayCtx via StartTTLRefresh after upgrade.
	src, err := os.ReadFile("sandbox_desktop_ws.go")
	require.NoError(t, err)
	text := string(src)
	require.NotContains(t, text, "ttlRefresh func")
	require.NotContains(t, text, "go r.refreshTTL")
	require.NotContains(t, text, "func (r *desktopRelay) refreshTTL")
}

func TestDesktopRelaySandboxDropReleasesWithoutReadTimeout(t *testing.T) {
	// The Redis desktop slot lives until run() returns. If the sandbox hop
	// dies and run() waits out desktopReadTimeout on the still-open browser
	// read, every reconnect is DESKTOP_BUSY for up to two minutes.
	browserClient, sandboxClient, runReturned, _ := startDesktopRelay(t, nil)

	require.NoError(t, sandboxClient.Close())

	select {
	case <-runReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("run() still blocked after the sandbox hop dropped; " +
			"the desktop slot would stay held for desktopReadTimeout")
	}

	require.NoError(t, browserClient.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err := browserClient.ReadMessage()
	require.Error(t, err, "the browser hop must close so noVNC disconnects and can reconnect")
}

func TestDesktopRelayUnblocksWhenContextCanceled(t *testing.T) {
	// cancel() alone does not unblock gorilla ReadMessage. watchContext must
	// closePeers or run() sits on desktopReadTimeout and the Redis slot stays held.
	_, _, runReturned, cancel := startDesktopRelay(t, nil)
	cancel()

	select {
	case <-runReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("run() still blocked after ctx cancel; watchContext must close both hops")
	}
}

func startDesktopRelay(t *testing.T, authCheck func(context.Context) error) (
	browserClient, sandboxClient *websocket.Conn, runReturned <-chan struct{}, cancel context.CancelFunc,
) {
	t.Helper()

	browserSrvConn := make(chan *websocket.Conn, 1)
	sandboxSrvConn := make(chan *websocket.Conn, 1)
	browserSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := desktopUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		browserSrvConn <- conn
	}))
	t.Cleanup(browserSrv.Close)
	sandboxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := desktopUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sandboxSrvConn <- conn
	}))
	t.Cleanup(sandboxSrv.Close)

	var err error
	browserClient, _, err = websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(browserSrv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = browserClient.Close() })
	sandboxClient, _, err = websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(sandboxSrv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sandboxClient.Close() })

	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())
	t.Cleanup(cancel)
	relay := &desktopRelay{
		browser:        <-browserSrvConn,
		sandbox:        <-sandboxSrvConn,
		ctx:            ctx,
		cancel:         cancel,
		sessionID:      "sess-pump",
		idleDisconnect: 0,
		authCheck:      authCheck,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.run()
	}()
	return browserClient, sandboxClient, done, cancel
}
