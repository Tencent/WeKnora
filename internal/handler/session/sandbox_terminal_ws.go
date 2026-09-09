// Package session: sandbox terminal WebSocket.
//
// GET /api/v1/sessions/:session_id/sandbox/terminal?ticket=<short-lived JWT>
//
// The route is registered before the global auth middleware because a
// browser WebSocket handshake cannot carry the Authorization header. The
// browser first POSTs (with a normal Bearer token) for a 2-minute ticket
// bound to this session and to that access-token's id, then presents the
// ticket on the handshake. After upgrade the bridge rechecks the bound
// token, user, workspace membership, and session ownership about once a
// minute and tears the PTY down on failure.
//
//	binary frame  client→server  raw keystrokes into the PTY
//	binary frame  server→client  raw PTY output
//	text frame    both ways      JSON control frames (resize/ping/ready/…)
package session

import (
	"context"
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// Terminal protocol tunables.
const (
	// terminalPingInterval / terminalReadTimeout drive the liveness loop:
	// the server pings, and any read (pong, keystroke, control frame)
	// refreshes the read deadline. The read window is deliberately 4x the
	// ping interval (not 2x) because a backgrounded browser tab throttles
	// timers — its pong reply can lag well past one interval, and a tight
	// window would drop terminals that are perfectly healthy.
	terminalPingInterval = 30 * time.Second
	terminalReadTimeout  = 120 * time.Second
	// terminalWriteTimeout bounds a single output-frame write so a stalled
	// browser tab cannot pin the pump goroutine forever.
	terminalWriteTimeout = 10 * time.Second
	// terminalMaxInputBytes caps one keystroke frame; terminals exchange
	// small chunks, so anything larger is abusive or buggy.
	terminalMaxInputBytes = 4096
	// terminalMaxGeometry caps the parsed initial geometry.
	terminalMaxGeometry = 500

	// terminalMaxPerSession caps concurrent PTYs per session to protect the
	// sandbox from resource exhaustion.
	terminalMaxPerSession = 5

	terminalAuthRecheckTimeout = 10 * time.Second
)

// terminalAuthRecheckInterval is how often an open PTY re-validates the
// minting access token, user, workspace membership, and session ownership.
// Handshake auth is a snapshot; this is what closes the window after logout,
// token revocation, or being removed from the workspace. Tests lower it.
var terminalAuthRecheckInterval = time.Minute

// Protocol error codes sent as HTTP close reasons and on error frames.
const (
	terminalErrNotBound    = "SANDBOX_NOT_BOUND"
	terminalErrPaused      = "SANDBOX_PAUSED"
	terminalErrUnsupported = "TERMINAL_UNSUPPORTED"
	terminalErrInternal    = "INTERNAL"
	terminalErrIdle        = "IDLE_DISCONNECTED"
	terminalErrAuth        = "AUTH_REVOKED"
)

// terminalUpgrader performs the WebSocket handshake.
//
// Origin is NOT checked here on purpose: authentication travels explicitly
// in the short-lived ticket query parameter and is validated before the
// upgrade, and the endpoint uses no ambient credentials (cookies), so a
// cross-origin browser tab cannot piggyback a victim session. A strict
// same-origin check would also break the dev setup, where the vite proxy
// rewrites Host to the backend address while Origin stays on the dev server.
var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// terminalLimiter caps concurrent PTYs per session across this process.
type terminalLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

var sessionTerminalLimiter = &terminalLimiter{counts: make(map[string]int)}

// acquire reserves a terminal slot. The returned release func is always
// non-nil and idempotent.
func (l *terminalLimiter) acquire(sessionID string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[sessionID] >= terminalMaxPerSession {
		return func() {}, false
	}
	l.counts[sessionID]++
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if !released {
			released = true
			l.counts[sessionID]--
			if l.counts[sessionID] <= 0 {
				delete(l.counts, sessionID)
			}
		}
	}, true
}

// terminalControlFrame is a text-mode protocol frame.
type terminalControlFrame struct {
	Type string `json:"type"`
	// Code carries the error code on error frames.
	Code string `json:"code,omitempty"`
	// Message is a human-readable detail for error frames.
	Message string `json:"message,omitempty"`
	// PID is the remote shell process id on ready frames.
	PID uint32 `json:"pty_id,omitempty"`
	// Backend names the sandbox provider on ready frames.
	Backend string `json:"backend,omitempty"`
	// ExitCode is set on exited frames (-1 when the provider didn't say).
	ExitCode *int `json:"exit_code,omitempty"`
	// Cols/Rows carry resize requests.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// SandboxTerminalWS upgrades a browser connection into a session sandbox
// terminal. See the file header for the protocol.
func (h *Handler) SandboxTerminalWS(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Param("session_id"))
	}
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session id is required"})
		return
	}

	claims, err := service.ParseSandboxTerminalTicket(c.Query("ticket"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid or expired ticket"})
		return
	}
	if claims.SessionID != sessionID {
		c.JSON(http.StatusForbidden, gin.H{"error": "ticket is not valid for this session"})
		return
	}
	// One call covers the whole handshake identity: minting token still live,
	// user active, workspace membership intact, session still owned. It also
	// hands back the user, so nothing below re-queries it.
	user, err := h.checkTerminalAuth(c.Request.Context(), *claims, true)
	if err != nil {
		if stderrors.Is(err, service.ErrTerminalAuthDenied) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid or expired ticket"})
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

	release, ok := sessionTerminalLimiter.acquire(sessionID)
	if !ok {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many terminals for this session"})
		return
	}
	defer release()

	conn, err := terminalUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error response.
		return
	}

	// Provisioning is opt-in per connect, because it creates (or resumes and
	// re-bills) a microVM and this is a GET. A bare handshake — panel opened,
	// tab restored, background reconnect — stays lookup-only: a running
	// sandbox is attached, a paused one reports SANDBOX_PAUSED, and a
	// missing one reports SANDBOX_NOT_BOUND. Only a confirmed click sets
	// provision=1, which may create or resume.
	allowProvision := terminalFlagParam(c.Query("provision"))
	sandboxConfigID := h.terminalProvisionConfigID(ctx, c, allowProvision)

	// Geometry from the query string; the frontend resizes right after
	// ready anyway, so defaults only shape the first paint.
	opts := sandbox.RemoteTerminalOptions{
		Cols:      terminalSizeParam(c.Query("cols")),
		Rows:      terminalSizeParam(c.Query("rows")),
		AttachPID: terminalPIDParam(c.Query("pty_id")),
	}

	// The connection outlives the HTTP exchange, so the terminal lifetime is
	// governed by this derived context, cancelled in cleanup().
	termCtx, cancelTerm := context.WithCancel(context.WithoutCancel(ctx))
	terminal, err := h.openTerminal(termCtx, sessionID, sandboxConfigID, allowProvision, opts)
	if err != nil {
		code, detail := terminalErrorFrame(err)
		logger.Warnf(ctx, "[sandbox-terminal] open failed session=%s provision=%t code=%s: %v",
			sessionID, allowProvision, code, detail)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, code),
			time.Now().Add(terminalWriteTimeout))
		_ = conn.Close()
		cancelTerm()
		return
	}

	logger.Infof(ctx, "[sandbox-terminal] opened session=%s backend=%s pid=%d",
		sessionID, terminal.Backend, terminal.Session.PID())

	authClaims := *claims
	bridge := &terminalBridge{
		conn:           conn,
		ctx:            termCtx,
		cancel:         cancelTerm,
		session:        sessionID,
		terminal:       terminal,
		idleDisconnect: terminal.IdleDisconnect,
		authCheck: func(checkCtx context.Context) error {
			_, err := h.checkTerminalAuth(checkCtx, authClaims, false)
			return err
		},
	}
	bridge.run()
}

// openTerminal picks the lookup-only or the provisioning entry point. The
// split is the whole point of the provision flag: only a confirmed user
// action may create infrastructure.
func (h *Handler) openTerminal(
	ctx context.Context,
	sessionID, sandboxConfigID string,
	allowProvision bool,
	opts sandbox.RemoteTerminalOptions,
) (*service.SessionTerminal, error) {
	if allowProvision {
		return h.terminalService.EnsureSessionTerminal(ctx, sessionID, sandboxConfigID, opts)
	}
	return h.terminalService.OpenSessionTerminal(ctx, sessionID, opts)
}

// terminalProvisionConfigID resolves the sandbox config a confirmed create
// should use. It goes through resolveAgent so a shared agent from another
// workspace provisions the same way a chat turn would, instead of looking the
// agent up only in the current tenant.
func (h *Handler) terminalProvisionConfigID(ctx context.Context, c *gin.Context, allowProvision bool) string {
	if !allowProvision || h == nil || c == nil {
		return ""
	}
	agentID := strings.TrimSpace(c.Query("agent_id"))
	if agentID == "" {
		return ""
	}
	agent, _, _ := h.resolveAgent(ctx, c, agentID, terminalTenantParam(c.Query("agent_source_tenant_id")))
	if agent == nil {
		return ""
	}
	return strings.TrimSpace(agent.Config.SandboxConfigID)
}

func terminalTenantParam(raw string) uint64 {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || value == 0 {
		return 0
	}
	return value
}

// terminalFlagParam parses an opt-in query flag. Anything other than an
// explicit truthy value is false, so a missing or malformed parameter can
// never authorise a side effect.
func terminalFlagParam(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// terminalSizeParam parses a non-negative geometry query parameter.
func terminalSizeParam(raw string) uint32 {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || value <= 0 || value > terminalMaxGeometry {
		return 0
	}
	return uint32(value)
}

func terminalPIDParam(raw string) uint32 {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || value == 0 {
		return 0
	}
	return uint32(value)
}

// IssueSandboxTerminalTicket mints a short-lived handshake ticket for the
// WebSocket. The access JWT stays on this authenticated POST and never
// appears in the WS URL (or nginx access logs of that handshake).
func (h *Handler) IssueSandboxTerminalTicket(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Param("id"))
	}
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
	ticket, err := service.IssueSandboxTerminalTicket(userID, tenantID, sessionID, record.ID, 0)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue ticket"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"ticket":     ticket,
			"expires_in": int(service.DefaultSandboxTerminalTicketTTL.Seconds()),
		},
	})
}

// terminalErrorFrame maps an OpenSessionTerminal failure onto a protocol
// error code and a short message.
func terminalErrorFrame(err error) (code, message string) {
	switch {
	case stderrors.Is(err, sandbox.ErrNoLiveSessionSandbox):
		return terminalErrNotBound, "session has no live sandbox"
	case stderrors.Is(err, sandbox.ErrSandboxPaused):
		return terminalErrPaused, "session sandbox is paused"
	case stderrors.Is(err, service.ErrTerminalUnsupported):
		return terminalErrUnsupported, "sandbox backend does not support terminals"
	default:
		return terminalErrInternal, "failed to open terminal"
	}
}

func sandboxTerminalBearerToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func (h *Handler) checkTerminalAuth(
	ctx context.Context,
	claims service.SandboxTerminalTicketClaims,
	rejectExpired bool,
) (*types.User, error) {
	if h == nil {
		return nil, service.ErrTerminalAuthDenied
	}
	return service.CheckSandboxTerminalAuth(
		ctx,
		h.userService,
		h.memberService,
		h.sessionService,
		claims,
		h.terminalRBACEnforced(),
		rejectExpired,
	)
}

func (h *Handler) terminalRBACEnforced() bool {
	return h != nil && h.config != nil && h.config.Tenant.IsRBACEnforced()
}
