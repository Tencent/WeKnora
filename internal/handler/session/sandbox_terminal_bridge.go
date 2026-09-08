// Package session: terminal bridge between the WebSocket and the sandbox
// PTY (see sandbox_terminal_ws.go for the protocol contract).
package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
)

// terminalBridge owns one upgraded terminal connection. Exactly one of the
// goroutines it starts (output pump, input pump, heartbeat, idle, auth recheck)
// observes termination and drives cleanup; the others exit as a side effect of
// the shared teardown.
type terminalBridge struct {
	conn     *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	session  string
	terminal *service.SessionTerminal
	// idleDisconnect is how long to wait without PTY activity. Zero disables
	// the watcher (should not happen: the service always supplies a default).
	idleDisconnect time.Duration
	lastActivity   atomic.Int64
	// authCheck re-validates the handshake identity. It returns
	// service.ErrTerminalAuthDenied when access is definitively gone and any
	// other error for a lookup failure worth retrying. Nil disables the
	// watcher. A closure rather than the Handler: the bridge needs exactly
	// this one capability, and holding the handler would give it the whole
	// dependency graph (and make watchAuth untestable).
	authCheck func(context.Context) error

	// writeMu serialises writes: the output pump streams PTY bytes while
	// control frames (ready/error/exited) come from the pump goroutines.
	writeMu sync.Mutex
	// teardown runs once, from whichever goroutine observes the end.
	teardownOnce sync.Once
	// teardownReason records why the bridge ended, for the closing log line.
	teardownReason string
}

// teardownWith records the reason and tears the bridge down.
func (b *terminalBridge) teardownWith(reason string) {
	b.teardownOnce.Do(func() {
		b.teardownReason = reason
		b.teardownLocked()
	})
}

func (b *terminalBridge) run() {
	b.sendControl(terminalControlFrame{
		Type:    "ready",
		PID:     b.terminal.Session.PID(),
		Backend: b.terminal.Backend,
	})

	// Liveness defaults; the read loop also refreshes on every frame.
	b.conn.SetReadLimit(terminalMaxInputBytes)
	_ = b.conn.SetReadDeadline(time.Now().Add(terminalReadTimeout))
	b.conn.SetPongHandler(func(string) error {
		return b.conn.SetReadDeadline(time.Now().Add(terminalReadTimeout))
	})
	b.touchActivity()

	done := make(chan struct{})
	go b.pumpOutput(done)
	go b.heartbeat(done)
	go b.watchIdle(done)
	go b.watchAuth(done)
	b.pumpInput()
	b.teardownWith("client_disconnected")
	<-done
}

// sendControl writes one JSON control frame. Safe for concurrent use.
func (b *terminalBridge) sendControl(frame terminalControlFrame) {
	payload, err := json.Marshal(frame)
	if err != nil {
		return
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	// The deadline is set under the same lock as the write: it is connection
	// state, and the output pump shares this connection.
	_ = b.conn.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
	if werr := b.conn.WriteMessage(websocket.TextMessage, payload); werr != nil {
		logger.Debugf(b.ctx, "[sandbox-terminal] control write failed session=%s: %v",
			b.session, werr)
	}
}

// pumpOutput streams PTY output to the browser as binary frames. It exits
// when the terminal's output channel closes; teardown has already been
// triggered by then (or triggers now via the closing path in run()).
func (b *terminalBridge) pumpOutput(done chan struct{}) {
	defer close(done)
	// Normal termination: the channel closed because we tore the bridge down
	// from another goroutine. Any other exit path below sets a real reason.
	defer b.teardownWith("stream_closed")
	for event := range b.terminal.Session.Output() {
		switch {
		case event.Err != nil:
			logger.Warnf(b.ctx, "[sandbox-terminal] stream error session=%s: %v",
				b.session, event.Err)
			b.sendControl(terminalControlFrame{
				Type:    "error",
				Code:    terminalErrInternal,
				Message: "terminal stream failed",
			})
			b.teardownWith("stream_error")
			return
		case event.Exited:
			code := event.ExitCode
			b.sendControl(terminalControlFrame{Type: "exited", ExitCode: &code})
			b.teardownWith("shell_exited")
			return
		case len(event.Data) > 0:
			b.touchActivity()
			b.writeMu.Lock()
			_ = b.conn.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
			if err := b.conn.WriteMessage(websocket.BinaryMessage, event.Data); err != nil {
				b.writeMu.Unlock()
				logger.Debugf(b.ctx, "[sandbox-terminal] output write failed session=%s: %v",
					b.session, err)
				b.teardownWith("output_write_failed")
				return
			}
			b.writeMu.Unlock()
		}
	}
}

// pumpInput reads browser frames until the connection dies: binary frames
// are keystrokes, text frames are resize/ping control requests.
func (b *terminalBridge) pumpInput() {
	for {
		messageType, payload, err := b.conn.ReadMessage()
		if err != nil {
			// Normal close or transport failure — both end the session.
			return
		}
		_ = b.conn.SetReadDeadline(time.Now().Add(terminalReadTimeout))
		switch messageType {
		case websocket.BinaryMessage:
			b.touchActivity()
			writeCtx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
			werr := b.terminal.Session.Write(writeCtx, payload)
			cancel()
			if werr != nil {
				logger.Debugf(b.ctx, "[sandbox-terminal] input failed session=%s: %v",
					b.session, werr)
			}
		case websocket.TextMessage:
			var frame terminalControlFrame
			if json.Unmarshal(payload, &frame) != nil {
				continue
			}
			switch frame.Type {
			case "resize":
				cols, rows := frame.Cols, frame.Rows
				if cols <= 0 || rows <= 0 || cols > terminalMaxGeometry || rows > terminalMaxGeometry {
					continue
				}
				b.touchActivity()
				resizeCtx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
				rerr := b.terminal.Session.Resize(resizeCtx, uint32(cols), uint32(rows))
				cancel()
				if rerr != nil {
					logger.Debugf(b.ctx, "[sandbox-terminal] resize failed session=%s: %v",
						b.session, rerr)
				}
			case "ping":
				b.sendControl(terminalControlFrame{Type: "pong"})
			}
		}
	}
}

func (b *terminalBridge) touchActivity() {
	b.lastActivity.Store(time.Now().UnixNano())
}

func (b *terminalBridge) lastActivityTime() time.Time {
	n := b.lastActivity.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// terminalIdleExpired reports whether last activity is older than idle.
// A zero last timestamp never expires so the watcher cannot fire before the
// first touchActivity in run().
func terminalIdleExpired(last time.Time, idle time.Duration, now time.Time) bool {
	if idle <= 0 || last.IsZero() {
		return false
	}
	return now.Sub(last) >= idle
}

func (b *terminalBridge) watchIdle(done <-chan struct{}) {
	idle := b.idleDisconnect
	if idle <= 0 {
		return
	}
	tick := idle / 12
	if tick < 200*time.Millisecond {
		tick = 200 * time.Millisecond
	}
	if tick > 5*time.Second {
		tick = 5 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if terminalIdleExpired(b.lastActivityTime(), idle, time.Now()) {
				b.disconnectIdle()
				return
			}
		}
	}
}

func (b *terminalBridge) disconnectIdle() {
	b.sendControl(terminalControlFrame{
		Type:    "error",
		Code:    terminalErrIdle,
		Message: "terminal idle disconnect",
	})
	_ = b.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, terminalErrIdle),
		time.Now().Add(terminalWriteTimeout),
	)
	b.teardownWith("idle_disconnected")
}

func (b *terminalBridge) watchAuth(done <-chan struct{}) {
	if b.authCheck == nil {
		return
	}
	ticker := time.NewTicker(terminalAuthRecheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(b.ctx, terminalAuthRecheckTimeout)
			err := b.authCheck(checkCtx)
			cancel()
			if err == nil {
				continue
			}
			if b.ctx.Err() != nil {
				return
			}
			if errors.Is(err, service.ErrTerminalAuthDenied) {
				b.disconnectAuth()
				return
			}
			logger.Warnf(b.ctx, "[sandbox-terminal] auth recheck failed session=%s: %v",
				b.session, err)
		}
	}
}

func (b *terminalBridge) disconnectAuth() {
	b.sendControl(terminalControlFrame{
		Type:    "error",
		Code:    terminalErrAuth,
		Message: "terminal authorization revoked",
	})
	_ = b.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, terminalErrAuth),
		time.Now().Add(terminalWriteTimeout),
	)
	b.teardownWith("auth_revoked")
}

// heartbeat pings the browser so dead TCP connections surface within
// terminalReadTimeout instead of holding PTYs open forever.
func (b *terminalBridge) heartbeat(done chan struct{}) {
	ticker := time.NewTicker(terminalPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			// WriteControl is the only gorilla write documented as safe
			// concurrently with other writers: pumpOutput streams PTY bytes
			// at the same time, and two concurrent WriteMessage calls would
			// corrupt the connection state (and, once c.writeErr is set,
			// poison every later write — the premature-disconnect symptom).
			if err := b.conn.WriteControl(
				websocket.PingMessage,
				nil,
				time.Now().Add(terminalWriteTimeout),
			); err != nil {
				b.teardownWith("ping_failed")
				return
			}
		}
	}
}

// teardownLocked disconnects the PTY, closes the socket, and releases the
// session slot. Callers must go through teardownWith so the reason is logged.
func (b *terminalBridge) teardownLocked() {
	b.cancel()
	// Close only disconnects WeKnora from the PTY; the shell keeps running in
	// the sandbox and can be reattached provider-side.
	if err := b.terminal.Session.Close(); err != nil {
		logger.Debugf(b.ctx, "[sandbox-terminal] close failed session=%s: %v",
			b.session, err)
	}
	_ = b.conn.Close()
	logger.Infof(b.ctx, "[sandbox-terminal] closed session=%s reason=%s",
		b.session, b.teardownReason)
}
