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

func TestDesktopSlotIsExclusivePerSession(t *testing.T) {
	first, ok := sessionDesktopLimiter.acquire("sess-excl")
	require.True(t, ok)
	_, ok = sessionDesktopLimiter.acquire("sess-excl")
	require.False(t, ok, "a second desktop on one session means two people on one keyboard")
	first()
	third, ok := sessionDesktopLimiter.acquire("sess-excl")
	require.True(t, ok, "releasing must free the slot")
	third()
}

func TestDesktopSlotReleaseIsIdempotent(t *testing.T) {
	release, ok := sessionDesktopLimiter.acquire("sess-idem")
	require.True(t, ok)
	release()
	release()
	next, ok := sessionDesktopLimiter.acquire("sess-idem")
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

func newDesktopSlotHandler(t *testing.T) (*Handler, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return &Handler{redis: client}, mini, client
}

func TestDesktopSlotRenewSurvivesTransientRedisError(t *testing.T) {
	withDesktopSlotTiming(t, 400*time.Millisecond, 50*time.Millisecond)
	h, mini, client := newDesktopSlotHandler(t)
	ctx := context.Background()

	release, got, hold, err := h.acquireDesktopSlot(ctx, "sess-blip")
	require.NoError(t, err)
	require.True(t, got)
	require.NotNil(t, hold)
	defer release()

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
	require.NotEmpty(t, client.Get(ctx, desktopSlotKey("sess-blip")).Val())

	_, got, _, err = h.acquireDesktopSlot(ctx, "sess-blip")
	require.NoError(t, err)
	require.False(t, got, "the original relay must still own the slot after a blip")
}

func TestDesktopSlotCancelsHoldWhenRedisStaysDownPastLease(t *testing.T) {
	withDesktopSlotTiming(t, 200*time.Millisecond, 40*time.Millisecond)
	h, mini, _ := newDesktopSlotHandler(t)
	ctx := context.Background()

	release, got, hold, err := h.acquireDesktopSlot(ctx, "sess-down")
	require.NoError(t, err)
	require.True(t, got)
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

	release, got, hold, err := h.acquireDesktopSlot(ctx, "sess-stolen")
	require.NoError(t, err)
	require.True(t, got)
	defer release()

	require.NoError(t, client.Set(ctx, desktopSlotKey("sess-stolen"), "intruder", time.Minute).Err())
	select {
	case <-hold.Done():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("losing the redis key must cancel the hold so the relay can drop")
	}

	second, got, secondHold, err := h.acquireDesktopSlot(ctx, "sess-stolen")
	require.NoError(t, err)
	require.False(t, got, "the thief still owns the key; we must not overwrite it")
	_ = second
	_ = secondHold
}

func TestDesktopSlotCancelsHoldWhenKeyExpires(t *testing.T) {
	withDesktopSlotTiming(t, 400*time.Millisecond, 40*time.Millisecond)
	h, _, client := newDesktopSlotHandler(t)
	ctx := context.Background()

	release, got, hold, err := h.acquireDesktopSlot(ctx, "sess-expired")
	require.NoError(t, err)
	require.True(t, got)
	defer release()

	require.NoError(t, client.Del(ctx, desktopSlotKey("sess-expired")).Err())
	select {
	case <-hold.Done():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("an expired slot must cancel the hold; otherwise a second client can join")
	}

	second, got, secondHold, err := h.acquireDesktopSlot(ctx, "sess-expired")
	require.NoError(t, err)
	require.True(t, got, "after the first hold is lost the session must accept one new desktop")
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
	require.Contains(t, text, `h.terminalProvisionPin(ensureCtx, c, allowProvision)`)
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
