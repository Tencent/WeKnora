package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var browserAuthRecheckInterval = 30 * time.Second

var sessionBrowserLimiter = &terminalLimiter{counts: make(map[string]int)}

const (
	browserStreamMaxFrame = 4 * 1024 * 1024
	browserStreamPrefix   = "WK_BROWSER_STREAM "
)

// SandboxBrowserStream accepts a browser-only ticket. A browser ticket cannot
// open a terminal; neither endpoints nor shell input are accepted here.
func (h *Handler) SandboxBrowserStream(c *gin.Context) {
	sessionID := c.Param("id")
	claims, err := service.ParseSandboxBrowserTicket(c.Query("ticket"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid browser ticket"})
		return
	}
	if claims.SessionID != sessionID {
		c.JSON(http.StatusForbidden, gin.H{"error": "ticket session mismatch"})
		return
	}
	user, err := h.checkTerminalAuth(c.Request.Context(), *claims, true)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "browser access denied"})
		return
	}
	if !middleware.AttachAuthenticatedUser(c, h.tenantService, h.memberService, h.config, user, claims.TenantID) {
		return
	}
	token := c.Query("control_token")
	if len(token) < 24 || len(token) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid control token"})
		return
	}
	if h.terminalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sandbox unavailable"})
		return
	}
	release, ok := sessionBrowserLimiter.acquire(sessionID)
	if !ok {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many browser streams"})
		return
	}
	defer release()
	conn, err := terminalUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	opening := time.AfterFunc(20*time.Second, cancel)
	stream, err := h.terminalService.OpenBrowserStream(ctx, sessionID, token)
	opening.Stop()
	if err != nil {
		_ = conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer done()
		_ = stream.Write(cleanup, []byte("{\"type\":\"disconnect\"}\n"))
		_ = stream.Close()
	}()
	bridgeBrowserStream(ctx, cancel, conn, stream, token, func(ctx context.Context) error {
		_, err := h.checkTerminalAuth(ctx, *claims, true)
		return err
	})
}

type browserStreamInput struct {
	Type    string                  `json:"type"`
	ID      int64                   `json:"id,omitempty"`
	Seq     int64                   `json:"seq,omitempty"`
	Command *service.BrowserCommand `json:"command,omitempty"`
}

func validateBrowserStreamInput(raw []byte, token string) ([]byte, error) {
	var input browserStreamInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch input.Type {
	case "ack":
		if input.Seq < 0 {
			return nil, fmt.Errorf("invalid frame sequence")
		}
		input.Command = nil
		input.ID = 0
	case "ping":
		input.Command = nil
		input.ID = 0
		input.Seq = 0
	case "command":
		if input.ID <= 0 || input.Command == nil {
			return nil, fmt.Errorf("invalid browser command")
		}
		if err := input.Command.Validate(); err != nil {
			return nil, err
		}
		input.Command.Token = token
		input.Command.Stream = true
		input.Seq = 0
	default:
		return nil, fmt.Errorf("unsupported stream input")
	}
	payload, err := json.Marshal(input)
	return append(payload, '\n'), err
}

func bridgeBrowserStream(
	ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn,
	stream sandbox.RemoteTerminalSession, token string, authorize func(context.Context) error,
) {
	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	go func() {
		defer cancel()
		for {
			kind, raw, err := conn.ReadMessage()
			if err != nil || kind != websocket.TextMessage {
				return
			}
			payload, err := validateBrowserStreamInput(raw, token)
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
			writeCtx, done := context.WithTimeout(ctx, 5*time.Second)
			err = stream.Write(writeCtx, payload)
			done()
			if err != nil {
				return
			}
		}
	}()
	authTick := time.NewTicker(browserAuthRecheckInterval)
	defer authTick.Stop()
	var pending []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-authTick.C:
			authCtx, done := context.WithTimeout(ctx, 10*time.Second)
			err := authorize(authCtx)
			done()
			if err != nil {
				return
			}
		case event, ok := <-stream.Output():
			if !ok || event.Err != nil || event.Exited {
				return
			}
			pending = append(pending, event.Data...)
			for {
				end := bytes.IndexByte(pending, '\n')
				if end < 0 {
					break
				}
				line := bytes.TrimSpace(pending[:end])
				pending = pending[end+1:]
				if !bytes.HasPrefix(line, []byte(browserStreamPrefix)) {
					continue
				}
				raw := line[len(browserStreamPrefix):]
				if len(raw) > browserStreamMaxFrame || !json.Valid(raw) {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
					return
				}
			}
			if len(pending) > browserStreamMaxFrame {
				return
			}
		}
	}
}
