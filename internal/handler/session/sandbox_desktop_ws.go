// Package session provides the sandbox desktop WebSocket relay.
//
// Browser (noVNC, no credentials)
//
//	⇄ this handler          ticket auth, 14-byte handshake skip, opcode relay
//	⇄ sandbox gateway       e2b-traffic-access-token + Authorization: Basic
//	⇄ websockify :6080      ⇄ x11vnc :5900 (127.0.0.1, -nopw) ⇄ Xvfb ⇄ XFCE
//
// Both credentials exist only on the backend→sandbox hop. The browser holds
// neither, which is the whole reason traffic is relayed rather than sent
// direct.
package session

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// desktopBrowserReadLimit bounds one client frame. 4096 (the terminal's
	// value) is not enough: a ClientCutText paste exceeds it easily, and
	// gorilla closes the connection on overrun rather than truncating.
	desktopBrowserReadLimit = 64 * 1024

	// desktopSandboxReadLimit bounds one framebuffer-update frame. websockify
	// buffers 65536 by default, so 64 KiB would sit exactly on the edge.
	desktopSandboxReadLimit = 1024 * 1024

	desktopReadTimeout = 120 * time.Second
	// desktopHandshakeTimeout bounds the pre-upgrade RFB hello. The relay
	// read timeout (120s) was reused here and held the Redis slot for two
	// minutes whenever x11vnc was slow; every retry became DESKTOP_BUSY.
	desktopHandshakeTimeout = 10 * time.Second
	desktopWriteTimeout     = 10 * time.Second
	desktopPingInterval     = 30 * time.Second
)

// Close reasons. SANDBOX_REBUILT is desktop-specific and load-bearing: a
// skill install marks the binding stale, and a desktop reconnect with no
// AgentQA in flight rebuilds the sandbox immediately
// (session_lifecycle.go:190). The user did nothing; someone else clicked
// install in settings. Reconnecting silently into a blank desktop would hide
// that their /workspace scratch is gone.
const (
	desktopErrNotBound    = "SANDBOX_NOT_BOUND"
	desktopErrPaused      = "SANDBOX_PAUSED"
	desktopErrUnsupported = "DESKTOP_UNSUPPORTED"
	desktopErrStartFailed = "DESKTOP_START_FAILED"
	desktopErrBusy        = "DESKTOP_BUSY"
	desktopErrRebuilt     = "SANDBOX_REBUILT"
	desktopErrUpstream    = "DESKTOP_UPSTREAM"
	desktopErrIdle        = "IDLE_DISCONNECTED"
	desktopErrAuth        = "AUTH_REVOKED"
	desktopErrInternal    = "INTERNAL"
)

// desktopUpgrader mirrors terminalUpgrader's CheckOrigin decision and the
// reasoning behind it: authentication travels in an explicit one-shot ticket
// validated before the upgrade, and the endpoint uses no ambient credentials
// (no cookies), so a cross-origin tab cannot piggyback a victim session. A
// strict same-origin check would also break the vite dev proxy, which
// rewrites Host while Origin stays on the dev server.
//
// Buffers are 8x the terminal's because RFB framebuffer updates would
// otherwise be chopped into a great many small writes.
var desktopUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
	Subprotocols:    []string{"binary"},
}

// IssueSandboxDesktopTicket mints a one-shot handshake ticket. The access JWT
// stays on this authenticated POST and never reaches the WS URL.
func (h *Handler) IssueSandboxDesktopTicket(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := desktopSessionIDParam(c)
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session id is required"})
		return
	}
	if _, err := h.sessionService.GetOwnedSession(ctx, sessionID); err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load session"})
		return
	}
	userID, ok := types.UserIDFromContext(ctx)
	if !ok || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	accessToken := sandboxTerminalBearerToken(c)
	if accessToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: access token required"})
		return
	}
	record, err := h.userService.GetAccessTokenByValue(ctx, accessToken)
	if err != nil || record == nil || strings.TrimSpace(record.ID) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if err := service.AssertAccessTokenStillActive(record, userID, time.Now()); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	ticket, err := h.desktopTickets.Issue(ctx, service.SandboxDesktopTicketClaims{
		UserID: userID, TenantID: tenantID, SessionID: sessionID, TokenID: record.ID,
	})
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue ticket"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"ticket":     ticket,
		"expires_in": int(service.DefaultSandboxDesktopTicketTTL.Seconds()),
	}})
}

// SandboxDesktopWS relays the browser's RFB stream to the session sandbox.
//
// Ticket, auth, and the exclusive slot stay HTTP because those run before
// Ensure. Overlay states from Ensure (SANDBOX_NOT_BOUND / SANDBOX_PAUSED /
// DESKTOP_UNSUPPORTED / DESKTOP_START_FAILED) must upgrade and then close
// with a reason: the JS WebSocket API never exposes a failed handshake's
// HTTP status, so a 409 here becomes a generic error overlay instead of
// the confirm card. Handshake prelude failures stay HTTP — by then the
// sandbox is already live and the frontend must not show "create sandbox".
func (h *Handler) SandboxDesktopWS(c *gin.Context) {
	sessionID := desktopSessionIDParam(c)
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session id is required"})
		return
	}

	// 1. One-shot ticket. Auth reads the incoming request; workspace identity
	//    is attached next. Do not capture c.Request.Context() before
	//    AttachAuthenticatedUser — that snapshot has no tenant, and
	//    EnsureSessionDesktop then fails with "missing workspace context"
	//    (mapped to DESKTOP_UPSTREAM). SandboxTerminalWS binds ctx after attach
	//    for the same reason.
	claims, err := h.desktopTickets.Consume(c.Request.Context(), c.Query("ticket"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid or expired ticket"})
		return
	}
	if claims.SessionID != sessionID {
		c.JSON(http.StatusForbidden, gin.H{"error": "ticket is not valid for this session"})
		return
	}
	user, err := h.checkTerminalAuth(c.Request.Context(), service.SandboxTerminalTicketClaims{
		UserID: claims.UserID, TenantID: claims.TenantID,
		SessionID: claims.SessionID, TokenID: claims.TokenID,
	}, true)
	if err != nil {
		if stderrors.Is(err, service.ErrTerminalAuthDenied) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{"session_id": sessionID})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate ticket"})
		return
	}
	if !middleware.AttachAuthenticatedUser(
		c, h.tenantService, h.memberService, h.config, user, claims.TenantID,
	) {
		return
	}
	ctx := c.Request.Context()

	// 2. One desktop per session, across replicas. Unlike the terminal's
	//    5-per-session in-process counter, this must be distributed: two
	//    browsers on one desktop means two people fighting over one keyboard.
	releaseSlot, acquired, slotHold, err := h.acquireDesktopSlot(ctx, sessionID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reserve desktop"})
		return
	}
	if !acquired {
		c.JSON(http.StatusConflict, gin.H{"error": desktopErrBusy})
		return
	}
	defer releaseSlot()

	// 3. Start, dial. Provisioning is opt-in per connect, because it creates
	//    (or resumes and re-bills) a microVM and this is a GET. A bare
	//    handshake — panel opened, tab restored, background reconnect —
	//    stays lookup-only: a running sandbox is attached, a paused one
	//    reports SANDBOX_PAUSED, and a missing one reports SANDBOX_NOT_BOUND.
	//    Only a confirmed click sets provision=1. Use the request context so
	//    a browser that gives up the handshake (and every DESKTOP_BUSY retry)
	//    cancels this work and releases the slot. The relay context is
	//    detached only after the browser socket is upgraded.
	ensureCtx := ctx
	if slotHold != nil {
		var cancelEnsure context.CancelFunc
		ensureCtx, cancelEnsure = context.WithCancel(ctx)
		stopEnsureWatch := context.AfterFunc(slotHold, cancelEnsure)
		defer stopEnsureWatch()
		defer cancelEnsure()
	}
	allowProvision := terminalFlagParam(c.Query("provision"))
	provisionID := h.terminalProvisionConfigID(ensureCtx, c, allowProvision)
	desktop, err := h.desktopService.EnsureSessionDesktop(ensureCtx, sessionID, provisionID)
	if err != nil {
		if slotHold != nil && slotHold.Err() != nil {
			c.JSON(http.StatusConflict, gin.H{"error": desktopErrBusy})
			return
		}
		_, code := desktopErrorStatus(err)
		logger.Warnf(ctx, "[sandbox-desktop] open failed session=%s provision=%t code=%s: %v",
			sessionID, allowProvision, code, err)
		browserConn, upErr := desktopUpgrader.Upgrade(c.Writer, c.Request, nil)
		if upErr != nil {
			return
		}
		writeDesktopProtocolClose(browserConn, code)
		return
	}

	// 4. Read and validate the server's ProtocolVersion while an HTTP status
	//    code is still expressible: not RFB 003.008 means the client side is
	//    13 bytes rather than 14 and the opcode parser would be off by one
	//    forever. Whatever was read is replayed after the upgrade.
	//
	//    Only the version. The server withholds the security-type list until
	//    the client answers with its own version, and that answer is noVNC's
	//    to send — waiting for it here deadlocks until the deadline and looks
	//    exactly like a dead upstream. The "None and only None" assertion
	//    moved onto the relay (see desktopRelay.serverStream).
	//
	//    A mute upstream is answered with 502 and nothing else. Tearing the
	//    stack down from here and redialling was tried and made it worse: the
	//    browser waits out a second Ensure inside the same upgrade, and the
	//    healing belongs in EnsureSessionDesktop, which runs before the slot
	//    matters.
	prelude, err := readDesktopServerHandshake(ctx, desktop.Conn)
	if err != nil {
		_ = desktop.Conn.Close()
		logger.Warnf(ctx, "[sandbox-desktop] bad server handshake session=%s: %v", sessionID, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": desktopErrUpstream})
		return
	}

	// 5. Only now upgrade the browser socket.
	browserConn, err := desktopUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		_ = desktop.Conn.Close()
		return
	}
	relayCtx, cancelRelay := context.WithCancel(context.WithoutCancel(ctx))
	// Replay the handshake the browser's noVNC is waiting for.
	_ = browserConn.SetWriteDeadline(time.Now().Add(desktopWriteTimeout))
	if err := browserConn.WriteMessage(websocket.BinaryMessage, prelude); err != nil {
		cancelRelay()
		_ = browserConn.Close()
		_ = desktop.Conn.Close()
		return
	}

	authClaims := service.SandboxTerminalTicketClaims{
		UserID: claims.UserID, TenantID: claims.TenantID,
		SessionID: claims.SessionID, TokenID: claims.TokenID,
	}
	relay := &desktopRelay{
		browser:        browserConn,
		sandbox:        desktop.Conn,
		ctx:            relayCtx,
		cancel:         cancelRelay,
		sessionID:      sessionID,
		idleDisconnect: desktopIdleDisconnect(desktop),
		authCheck: func(checkCtx context.Context) error {
			_, err := h.checkTerminalAuth(checkCtx, authClaims, false)
			return err
		},
	}
	// Anything the server coalesced onto its version is already the start of
	// the security-type list, and the pump will never see it again.
	if len(prelude) > rfbProtocolVersionBytes {
		if err := relay.serverStream.Observe(prelude[rfbProtocolVersionBytes:]); err != nil {
			logger.Warnf(ctx, "[sandbox-desktop] %v session=%s", err, sessionID)
			relay.closeWith(desktopErrUpstream)
			return
		}
	}

	// A skill install can rebuild the sandbox under a live session
	// (session_lifecycle.go:190). Closing with SANDBOX_REBUILT tells the
	// frontend the /workspace scratch is gone instead of silently showing
	// a blank desktop. The previous sandbox ID lives in Redis so a reconnect
	// on another replica still sees the rebuild.
	if h.desktopSandboxRebuilt(ctx, sessionID, desktop.SandboxID) {
		relay.closeWith(desktopErrRebuilt)
		return
	}

	if slotHold != nil {
		stopRelayWatch := context.AfterFunc(slotHold, func() {
			relay.closeWith(desktopErrBusy)
		})
		defer stopRelayWatch()
	}

	logger.Infof(ctx,
		"[sandbox-desktop][audit] open session=%s sandbox=%s user=%s tenant=%d backend=%s",
		sessionID, desktop.SandboxID, claims.UserID, claims.TenantID, desktop.Backend)

	if desktop.StartTTLRefresh != nil {
		desktop.StartTTLRefresh(relayCtx)
	}

	activeDesktopRelays.Store(sessionID, relay)
	defer activeDesktopRelays.Delete(sessionID)
	relay.run()
}

// desktopErrorStatus maps an EnsureSessionDesktop failure to an HTTP status
// and a stable code the frontend switches on.
func desktopErrorStatus(err error) (int, string) {
	switch {
	case stderrors.Is(err, service.ErrDesktopUnsupported),
		stderrors.Is(err, sandbox.ErrDesktopUnsupported):
		return http.StatusNotImplemented, desktopErrUnsupported
	case stderrors.Is(err, service.ErrDesktopStartFailed):
		return http.StatusServiceUnavailable, desktopErrStartFailed
	case stderrors.Is(err, sandbox.ErrNoLiveSessionSandbox):
		return http.StatusConflict, desktopErrNotBound
	case stderrors.Is(err, sandbox.ErrSandboxPaused):
		return http.StatusConflict, desktopErrPaused
	default:
		return http.StatusBadGateway, desktopErrUpstream
	}
}

// desktopIdleDisconnect is the same workspace window the terminal bridge
// uses. The desktop must not hard-code the 15-minute default: an admin who
// raised TerminalIdleDisconnectSec would otherwise see the two tabs disagree.
func desktopIdleDisconnect(d *service.SessionDesktop) time.Duration {
	if d != nil && d.IdleDisconnect > 0 {
		return d.IdleDisconnect
	}
	return sandbox.DefaultTerminalIdleDisconnect
}

func writeDesktopProtocolClose(conn *websocket.Conn, code string) {
	if conn == nil {
		return
	}
	reason := strings.TrimSpace(code)
	if reason == "" {
		reason = desktopErrInternal
	}
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
		time.Now().Add(desktopWriteTimeout),
	)
	_ = conn.Close()
}

func desktopSessionIDParam(c *gin.Context) string {
	if id := strings.TrimSpace(c.Param("id")); id != "" {
		return id
	}
	return strings.TrimSpace(c.Param("session_id"))
}

func readDesktopServerHandshake(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	if conn == nil {
		return nil, stderrors.New("desktop handshake: nil conn")
	}
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()
	defer close(handshakeDone)
	_ = conn.SetReadDeadline(time.Now().Add(desktopHandshakeTimeout))
	return rfbReadServerVersion(func() ([]byte, error) {
		msgType, payload, rerr := conn.ReadMessage()
		if rerr != nil {
			return nil, rerr
		}
		if msgType != websocket.BinaryMessage {
			return nil, nil
		}
		return payload, nil
	})
}

// acquireDesktopSlot reserves the session's single desktop slot across all
// replicas.
//
// The terminal's per-session cap is a process-local counter, which is
// harmless there (a second shell is a cosmetic problem) and unacceptable here
// (a second desktop is two people on one keyboard). Redis SET NX with a
// renewed lease is the cross-replica version; a crashed replica is covered by
// the lease expiry, not by the release call.
//
// Redis absent means Lite mode, which is one process, so the in-process
// limiter is the correct degradation there.
func (h *Handler) acquireDesktopSlot(
	ctx context.Context, sessionID string,
) (release func(), acquired bool, hold context.Context, err error) {
	if h.redis == nil {
		release, ok := sessionDesktopLimiter.acquire(sessionID)
		return release, ok, nil, nil
	}
	key := desktopSlotKey(sessionID)
	token, err := redislock.NewToken()
	if err != nil {
		return func() {}, false, nil, err
	}
	got, err := redislock.TryAcquire(ctx, h.redis, key, token, desktopSlotLease)
	if err != nil {
		return func() {}, false, nil, err
	}
	if !got {
		return func() {}, false, nil, nil
	}

	hold, cancelHold := context.WithCancelCause(context.Background())
	renewCtx, stopRenew := context.WithCancel(context.WithoutCancel(ctx))
	go renewDesktopSlot(renewCtx, cancelHold, h.redis, key, token)

	var once sync.Once
	return func() {
		once.Do(func() {
			stopRenew()
			cancelHold(nil)
			relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = redislock.Release(relCtx, h.redis, key, token)
			cancel()
		})
	}, true, hold, nil
}

func renewDesktopSlot(
	ctx context.Context,
	cancelHold context.CancelCauseFunc,
	client redis.UniversalClient,
	key, token string,
) {
	ticker := time.NewTicker(desktopSlotRenew)
	defer ticker.Stop()
	lastOK := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, rerr := redislock.Renew(ctx, client, key, token, desktopSlotLease)
			if rerr != nil {
				if stderrors.Is(rerr, context.Canceled) && ctx.Err() != nil {
					return
				}
				logger.Warnf(ctx, "[sandbox-desktop] slot renew failed key=%s: %v", key, rerr)
				if time.Since(lastOK) >= desktopSlotLease {
					cancelHold(rerr)
					return
				}
				continue
			}
			if !ok {
				cancelHold(redislock.ErrLockOwnershipLost)
				return
			}
			lastOK = time.Now()
		}
	}
}

var (
	// desktopSlotLease outlives a network blip; desktopSlotRenew keeps it
	// alive while the relay runs. Tests shorten both so a stolen or expired
	// key is observed without waiting on the production 90s lease.
	desktopSlotLease = 90 * time.Second
	desktopSlotRenew = 30 * time.Second
)

func desktopSlotKey(sessionID string) string {
	return "weknora:desktop-slot:" + sessionID
}

// desktopLimiter caps the session to one desktop within this process.
type desktopLimiter struct {
	mu   sync.Mutex
	open map[string]bool
}

var sessionDesktopLimiter = &desktopLimiter{open: make(map[string]bool)}

func (l *desktopLimiter) acquire(sessionID string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open[sessionID] {
		return func() {}, false
	}
	l.open[sessionID] = true
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if !released {
			released = true
			delete(l.open, sessionID)
		}
	}, true
}

// desktopRelay forwards frames in both directions and judges idleness from
// the client message stream.
type desktopRelay struct {
	browser   *websocket.Conn
	sandbox   *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	sessionID string

	stream rfbClientStream

	// serverStream carries the security-type assertion that used to run
	// before the upgrade. See rfbProtocolVersionBytes for why it had to move.
	serverStream rfbServerStream

	mu           sync.Mutex
	lastActivity time.Time

	// idleDisconnect is how long the desktop may go without a client
	// KeyEvent / PointerEvent / ClientCutText before the relay closes. It
	// reuses the workspace's terminal setting: both exist to let a sandbox
	// reach its provider TTL once the human walked away.
	idleDisconnect time.Duration

	// authCheck re-validates the handshake identity on the same cadence as
	// the terminal. Handshake is a snapshot; this is what closes the window
	// after logout, token revocation, or being removed from the workspace.
	// Nil disables the watcher. rejectExpired must stay false: axios rotates
	// access tokens without revoking the row the ticket was bound to.
	authCheck func(context.Context) error
}

func (r *desktopRelay) run() {
	defer r.cancel()
	defer func() { _ = r.browser.Close() }()
	defer func() { _ = r.sandbox.Close() }()

	r.browser.SetReadLimit(desktopBrowserReadLimit)
	r.sandbox.SetReadLimit(desktopSandboxReadLimit)
	r.browser.SetPongHandler(func(string) error {
		return r.browser.SetReadDeadline(time.Now().Add(desktopReadTimeout))
	})
	r.sandbox.SetPongHandler(func(string) error {
		return r.sandbox.SetReadDeadline(time.Now().Add(desktopReadTimeout))
	})
	r.touch()

	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	// The sandbox pump is not the goroutine run() waits on. If it just
	// returns, pumpBrowserToSandbox stays blocked on a live browser read
	// until desktopReadTimeout, and the Redis slot stays held (DESKTOP_BUSY
	// on every reconnect). Closing both hops unblocks that read immediately.
	go func() {
		defer finish()
		defer r.closePeers()
		r.pumpSandboxToBrowser()
	}()
	go r.watchIdle(done)
	go r.watchContext(done)
	go r.heartbeat(done)
	go r.watchAuth(done)

	r.pumpBrowserToSandbox()
	finish()
}

// pumpBrowserToSandbox forwards client frames and feeds the opcode parser.
//
// Note what is NOT here: ping/pong forwarding. gorilla does not surface
// received pings as messages, so each hop keeps its own liveness. That is the
// correct behaviour, and it is written down because it looks like an omission.
func (r *desktopRelay) pumpBrowserToSandbox() {
	for {
		_ = r.browser.SetReadDeadline(time.Now().Add(desktopReadTimeout))
		msgType, payload, err := r.browser.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		parsing := r.stream.ParsingEnabled()
		if r.stream.Consume(payload) {
			r.touch()
		}
		if parsing && !r.stream.ParsingEnabled() {
			logger.Warnf(r.ctx,
				"[sandbox-desktop] RFB client parser disabled; falling back to reported activity session=%s",
				r.sessionID)
		}
		_ = r.sandbox.SetWriteDeadline(time.Now().Add(desktopWriteTimeout))
		if err := r.sandbox.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			return
		}
	}
}

func (r *desktopRelay) pumpSandboxToBrowser() {
	for {
		_ = r.sandbox.SetReadDeadline(time.Now().Add(desktopReadTimeout))
		msgType, payload, err := r.sandbox.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		if err := r.serverStream.Observe(payload); err != nil {
			logger.Warnf(r.ctx, "[sandbox-desktop] %v session=%s", err, r.sessionID)
			r.closeWith(desktopErrUpstream)
			return
		}
		_ = r.browser.SetWriteDeadline(time.Now().Add(desktopWriteTimeout))
		if err := r.browser.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			return
		}
	}
}

func (r *desktopRelay) touch() {
	r.mu.Lock()
	r.lastActivity = time.Now()
	r.mu.Unlock()
}

func (r *desktopRelay) idleFor(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.lastActivity)
}

func (r *desktopRelay) isIdle(now time.Time) bool {
	if r.idleDisconnect <= 0 {
		return false
	}
	return r.idleFor(now) >= r.idleDisconnect
}

// watchIdle closes the relay once nobody has touched the desktop for
// idleDisconnect. closePeers cancels relayCtx, which stops the
// StartTTLRefresh loop started after upgrade — that is what finally lets
// the sandbox pause on its own provider timeout.
func (r *desktopRelay) watchIdle(done <-chan struct{}) {
	if r.idleDisconnect <= 0 {
		return
	}
	// Check often enough that the reported idle time is roughly honest,
	// cheaply enough that it is free.
	interval := r.idleDisconnect / 10
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			if r.isIdle(now) {
				r.closeWith(desktopErrIdle)
				return
			}
		}
	}
}

// watchContext unblocks run() when the relay ctx is canceled from outside
// the pumps — notably when the Redis slot hold is lost. closePeers is what
// unblocks ReadMessage; canceling ctx alone would sit on desktopReadTimeout.
func (r *desktopRelay) watchContext(done <-chan struct{}) {
	select {
	case <-done:
	case <-r.ctx.Done():
		r.closePeers()
	}
}

// heartbeat keeps the browser hop alive. Each hop pings independently: this
// relay does not forward ping/pong, because gorilla does not surface received
// pings as messages. See pumpBrowserToSandbox.
func (r *desktopRelay) heartbeat(done <-chan struct{}) {
	ticker := time.NewTicker(desktopPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			_ = r.browser.WriteControl(websocket.PingMessage, nil,
				time.Now().Add(desktopWriteTimeout))
			_ = r.sandbox.WriteControl(websocket.PingMessage, nil,
				time.Now().Add(desktopWriteTimeout))
		}
	}
}

// watchAuth re-validates the minting identity while the desktop is open.
// Denied (logout / revoke / membership loss) tears the relay down; any other
// error is a lookup failure and is retried, matching the terminal so a
// database hiccup cannot disconnect every open desktop at once.
func (r *desktopRelay) watchAuth(done <-chan struct{}) {
	if r == nil || r.authCheck == nil {
		return
	}
	ticker := time.NewTicker(terminalAuthRecheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(r.ctx, terminalAuthRecheckTimeout)
			err := r.authCheck(checkCtx)
			cancel()
			if err == nil {
				continue
			}
			if r.ctx.Err() != nil {
				return
			}
			if stderrors.Is(err, service.ErrTerminalAuthDenied) {
				r.closeWith(desktopErrAuth)
				return
			}
			logger.Warnf(r.ctx, "[sandbox-desktop] auth recheck failed session=%s: %v",
				r.sessionID, err)
		}
	}
}

// closeWith sends a close frame carrying a stable reason code, so the
// frontend can show the right message instead of silently reconnecting.
func (r *desktopRelay) closeWith(reason string) {
	if r != nil && r.browser != nil {
		_ = r.browser.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
			time.Now().Add(desktopWriteTimeout))
	}
	r.closePeers()
}

// closePeers tears down both hops. gorilla's Close unblocks a ReadMessage
// on the other side, which is what lets run() return (and release the
// desktop slot) instead of sitting on desktopReadTimeout.
func (r *desktopRelay) closePeers() {
	if r == nil {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.browser != nil {
		_ = r.browser.Close()
	}
	if r.sandbox != nil {
		_ = r.sandbox.Close()
	}
}

// TouchFromFrontend records the activity signal the browser POSTs. It is the
// only source once the opcode parser has fallen back (unknown opcode). While
// the parser is healthy this is a no-op: a client that kept posting would
// otherwise ride the TTL without generating KeyEvent/PointerEvent.
func (r *desktopRelay) TouchFromFrontend() {
	if r == nil || r.stream.ParsingEnabled() {
		return
	}
	r.touch()
}

// ReportSandboxDesktopActivity records browser-side key/mouse activity.
//
// It is the fallback the RFB parser degrades to on an unknown opcode, and a
// no-op when the parser is healthy. It is deliberately not the primary
// signal: a client that simply stops posting would keep the sandbox alive
// for free.
func (h *Handler) ReportSandboxDesktopActivity(c *gin.Context) {
	sessionID := desktopSessionIDParam(c)
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session id is required"})
		return
	}
	if _, err := h.sessionService.GetOwnedSession(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	touchDesktopRelay(sessionID)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// activeDesktopRelays lets the activity endpoint reach the live relay. It is
// process-local by nature: the POST and the WebSocket ride the same sticky
// connection in practice, and a miss only costs one skipped touch.
var activeDesktopRelays sync.Map // sessionID -> *desktopRelay

func touchDesktopRelay(sessionID string) {
	if value, ok := activeDesktopRelays.Load(sessionID); ok {
		if relay, ok := value.(*desktopRelay); ok {
			relay.TouchFromFrontend()
		}
	}
}

// desktopSandboxRebuilt records the sandbox this session's desktop just
// attached to and reports whether it differs from the previous one.
func (h *Handler) desktopSandboxRebuilt(ctx context.Context, sessionID, sandboxID string) bool {
	if h == nil || h.desktopLast == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	sandboxID = strings.TrimSpace(sandboxID)
	if sessionID == "" || sandboxID == "" {
		return false
	}
	prev, err := h.desktopLast.Get(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[sandbox-desktop] last-sandbox get failed session=%s: %v", sessionID, err)
	}
	if setErr := h.desktopLast.Set(ctx, sessionID, sandboxID); setErr != nil {
		logger.Warnf(ctx, "[sandbox-desktop] last-sandbox set failed session=%s: %v", sessionID, setErr)
	}
	return prev != "" && prev != sandboxID
}
