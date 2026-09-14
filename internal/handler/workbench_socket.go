package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type workbenchClientFrame struct {
	Type     string `json:"type"`
	Command  string `json:"command,omitempty"`
	Data     string `json:"data,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Cols     uint16 `json:"cols,omitempty"`
	Rows     uint16 `json:"rows,omitempty"`
}

func decodeWorkbenchFrame(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return service.ErrWorkbenchInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return service.ErrWorkbenchInvalid
	}
	return nil
}

// Terminal is registered before global bearer/API-key middleware. Its only
// credential is a single-use ticket in the first text frame, never in a URL.
func (h *WorkbenchHandler) Terminal(c *gin.Context) {
	if !h.Enabled() {
		workbenchHTTPError(c, service.ErrWorkbenchDisabled)
		return
	}
	if h.isShuttingDown() {
		workbenchHTTPError(c, service.ErrWorkbenchUnavailable)
		return
	}
	if c.Request.URL.RawQuery != "" {
		workbenchHTTPError(c, service.ErrWorkbenchInvalid)
		return
	}
	origin, err := h.requestOrigin(c.Request, true)
	if err != nil {
		workbenchHTTPError(c, err)
		return
	}
	select {
	case h.unauthenticated <- struct{}{}:
	default:
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	var releaseOnce sync.Once
	releaseSlot := func() { releaseOnce.Do(func() { <-h.unauthenticated }) }
	defer releaseSlot()
	upgrader := websocket.Upgrader{
		HandshakeTimeout: h.authTimeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      func(r *http.Request) bool { _, err := h.requestOrigin(r, true); return err == nil },
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	if !h.registerSocket(conn) {
		_ = conn.Close()
		return
	}
	defer func() {
		_ = conn.Close()
		h.unregisterSocket(conn)
	}()
	conn.SetReadLimit(service.WorkbenchMaxFrameBytes)
	// All outbound data goes through one writer. Application-level ping is
	// JSON; unsolicited protocol controls need no concurrent response writer.
	conn.SetPingHandler(func(string) error { return nil })
	conn.SetCloseHandler(func(int, string) error { return nil })
	authDeadline := time.Now().Add(h.authTimeout)
	_ = conn.SetReadDeadline(authDeadline)
	frameType, raw, err := conn.ReadMessage()
	if err != nil || frameType != websocket.TextMessage {
		return
	}
	var auth struct {
		Type   string `json:"type"`
		Ticket string `json:"ticket"`
	}
	if decodeWorkbenchFrame(raw, &auth) != nil || auth.Type != "auth" {
		return
	}
	authCtx, authCancel := context.WithDeadline(c.Request.Context(), authDeadline)
	defer authCancel()
	identity, err := h.service.ConsumeTicket(authCtx, auth.Ticket, origin)
	auth.Ticket = ""
	if err != nil {
		writeWorkbenchHandshakeError(conn, err, h.writeTimeout)
		return
	}
	lease, err := h.service.AcquireConsole(authCtx, identity)
	if err != nil {
		writeWorkbenchHandshakeError(conn, err, h.writeTimeout)
		return
	}
	releaseSlot()
	limits := service.DefaultWorkbenchLimits()
	ctx, cancel := context.WithTimeout(
		identity.Context(c.Request.Context()),
		time.Duration(limits.SessionTimeoutSeconds)*time.Second,
	)
	_ = conn.SetReadDeadline(time.Now().Add(time.Duration(limits.SessionTimeoutSeconds) * time.Second))
	console := &workbenchConsole{
		handler:  h,
		conn:     conn,
		ctx:      ctx,
		cancel:   cancel,
		identity: identity,
		lease:    lease,
		outgoing: make(chan workbenchOutput, 32),
		cols:     80,
		rows:     24,
	}
	if !h.registerConsole(console) {
		cancel()
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = lease.Release(releaseCtx)
		releaseCancel()
		writeWorkbenchHandshakeError(conn, service.ErrWorkbenchUnavailable, h.writeTimeout)
		return
	}
	defer func() {
		cancel()
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = lease.Release(releaseCtx)
		releaseCancel()
		h.unregisterConsole(console)
	}()
	console.run()
}

func writeWorkbenchHandshakeError(conn *websocket.Conn, err error, timeout time.Duration) {
	_, code, message := workbenchError(err)
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	_ = conn.WriteJSON(map[string]any{"type": "error", "code": code, "message": message})
}

type workbenchOutput struct {
	kind int
	data []byte
}
type workbenchCommand struct {
	cancel      context.CancelFunc
	execution   *service.WorkbenchExecution
	interrupted atomic.Bool
}
type workbenchConsole struct {
	handler     *WorkbenchHandler
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	identity    service.WorkbenchIdentity
	lease       *service.WorkbenchLease
	outgoing    chan workbenchOutput
	workers     sync.WaitGroup
	mu          sync.Mutex
	active      *workbenchCommand
	cols, rows  uint16
	outputBytes atomic.Int64
}

func (w *workbenchConsole) run() {
	w.workers.Add(2)
	go w.writeLoop()
	go w.recheckLoop()
	defer func() {
		w.cancel()
		_ = w.conn.Close() // Unblocks the reader as well as a stalled writer.
		w.workers.Wait()   // Command cleanup and completion audit precede lease release.
	}()
	w.json(map[string]any{"type": "ready", "limits": service.DefaultWorkbenchLimits()})
	for {
		kind, raw, err := w.conn.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			w.failure(service.ErrWorkbenchInvalid)
			return
		}
		var frame workbenchClientFrame
		if decodeWorkbenchFrame(raw, &frame) != nil {
			w.failure(service.ErrWorkbenchInvalid)
			return
		}
		checkCtx, cancel := context.WithTimeout(w.ctx, 2*time.Second)
		err = w.lease.Renew(checkCtx)
		cancel()
		if err != nil {
			w.failure(err)
			return
		}
		if w.ctx.Err() != nil {
			return
		}
		if !w.handle(frame) {
			return
		}
	}
}

func (w *workbenchConsole) enqueue(kind int, data []byte) bool {
	select {
	case <-w.ctx.Done():
		return false
	default:
	}
	select {
	case w.outgoing <- workbenchOutput{kind, data}:
		return true
	default:
		// A slow browser may not hold a process or grow an unbounded queue.
		w.cancel()
		return false
	}
}

func (w *workbenchConsole) json(value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		w.cancel()
		return false
	}
	return w.enqueue(websocket.TextMessage, data)
}

func (w *workbenchConsole) failure(err error) {
	_, code, message := workbenchError(err)
	w.json(map[string]any{"type": "error", "code": code, "message": message})
}

func (w *workbenchConsole) writeLoop() {
	defer w.workers.Done()
	defer func() { _ = w.conn.Close() }()
	defer w.cancel()
	for {
		select {
		case <-w.ctx.Done():
			return
		case frame := <-w.outgoing:
			_ = w.conn.SetWriteDeadline(time.Now().Add(w.handler.writeTimeout))
			if w.conn.WriteMessage(frame.kind, frame.data) != nil {
				return
			}
		}
	}
}

func (w *workbenchConsole) recheckLoop() {
	defer w.workers.Done()
	ticker := time.NewTicker(w.handler.recheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(w.ctx, 2*time.Second)
			err := w.lease.Renew(ctx)
			if err == nil {
				_, err = w.handler.service.Authorize(ctx, w.identity.SessionID)
			}
			cancel()
			if err != nil {
				w.failure(err)
				w.cancel()
				return
			}
		}
	}
}

func (w *workbenchConsole) handle(frame workbenchClientFrame) bool {
	if frame.Type != "command" && frame.Command != "" ||
		frame.Type != "stdin" && (frame.Data != "" || frame.Encoding != "") ||
		frame.Type != "resize" && (frame.Cols != 0 || frame.Rows != 0) {
		w.failure(service.ErrWorkbenchInvalid)
		return false
	}
	switch frame.Type {
	case "ping":
		return w.json(map[string]any{"type": "pong"})
	case "command":
		if strings.TrimSpace(frame.Command) == "" || len(frame.Command) > service.WorkbenchMaxCommandBytes {
			w.failure(service.ErrWorkbenchInvalid)
			return true
		}
		w.mu.Lock()
		if w.active != nil {
			w.mu.Unlock()
			w.failure(service.ErrWorkbenchBusy)
			return true
		}
		cols, rows := w.cols, w.rows
		ctx, cancel := context.WithTimeout(
			w.ctx,
			time.Duration(service.DefaultWorkbenchLimits().CommandTimeoutSeconds)*time.Second,
		)
		command := &workbenchCommand{cancel: cancel}
		w.active = command
		w.mu.Unlock()
		w.workers.Add(1)
		go w.execute(ctx, command, sandbox.CommandTerminalRequest{Command: frame.Command, Cols: cols, Rows: rows})
		return true
	case "resize":
		if frame.Cols == 0 || frame.Rows == 0 || frame.Cols > 500 || frame.Rows > 500 {
			w.failure(service.ErrWorkbenchInvalid)
			return true
		}
		w.mu.Lock()
		w.cols, w.rows = frame.Cols, frame.Rows
		w.mu.Unlock()
	case "stdin", "interrupt":
	default:
		w.failure(service.ErrWorkbenchInvalid)
		return false
	}
	w.mu.Lock()
	active := w.active
	var execution *service.WorkbenchExecution
	if active != nil {
		execution = active.execution
	}
	w.mu.Unlock()
	if execution == nil {
		if frame.Type == "interrupt" && active != nil {
			active.interrupted.Store(true)
			active.cancel()
			return true
		}
		if frame.Type != "resize" {
			w.failure(service.ErrWorkbenchInvalid)
		}
		return true
	}
	ctx, cancel := context.WithTimeout(w.ctx, 2*time.Second)
	defer cancel()
	var err error
	switch frame.Type {
	case "resize":
		err = execution.Terminal.Resize(ctx, frame.Cols, frame.Rows)
	case "stdin":
		var input []byte
		switch frame.Encoding {
		case "":
			input = []byte(frame.Data)
		case "base64":
			input, err = base64.StdEncoding.DecodeString(frame.Data)
		default:
			err = service.ErrWorkbenchInvalid
		}
		if err == nil {
			err = execution.Terminal.Input(ctx, input)
		}
	case "interrupt":
		err = execution.Terminal.Interrupt(ctx)
	}
	if err != nil {
		w.failure(err)
		active.cancel()
	}
	return true
}

func (w *workbenchConsole) execute(
	ctx context.Context,
	command *workbenchCommand,
	request sandbox.CommandTerminalRequest,
) {
	defer w.workers.Done()
	defer command.cancel()
	defer func() {
		w.mu.Lock()
		if w.active == command {
			w.active = nil
		}
		w.mu.Unlock()
	}()
	execution, err := w.handler.service.OpenTerminal(ctx, w.identity.SessionID, request)
	if err != nil {
		if command.interrupted.Load() {
			w.json(map[string]any{"type": "exit", "exit_code": 130, "reason": "interrupted"})
			return
		}
		w.failure(err)
		return
	}
	w.mu.Lock()
	command.execution = execution
	w.mu.Unlock()
	var closeOnce sync.Once
	var closeErr error
	closeTerminal := func() { closeOnce.Do(func() { closeErr = execution.Terminal.Close() }) }
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			closeTerminal()
		case <-finished:
		}
	}()
	w.json(map[string]any{"type": "started", "execution_id": execution.ExecutionID})
	buffer := make([]byte, 8192)
	var readErr error
	outputLimited := false
	for {
		n, err := execution.Terminal.Read(buffer)
		if n > 0 {
			if w.outputBytes.Add(int64(n)) > service.WorkbenchMaxOutputBytes {
				outputLimited = true
				w.cancel()
				closeTerminal()
				break
			}
			if !w.enqueue(websocket.BinaryMessage, append([]byte(nil), buffer[:n]...)) {
				readErr = service.ErrWorkbenchUnavailable
				w.cancel()
				closeTerminal()
				break
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
				command.cancel()
				closeTerminal()
			}
			break
		}
	}
	// Wait is bounded independently of a disconnect; Close explicitly kills
	// descendants, and the audit records unknown when cleanup cannot confirm it.
	waitCtx, waitCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	exit, waitErr := execution.Terminal.Wait(waitCtx)
	waitCancel()
	closeTerminal()
	close(finished)
	<-watcherDone
	operationErr := errors.Join(readErr, waitErr, closeErr)
	if outputLimited {
		exit = sandbox.CommandTerminalExit{ExitCode: 137, Reason: "output_limit"}
		if operationErr != nil {
			exit.ExitCode = -1
		}
	}
	exit = service.NormalizeWorkbenchTerminalExit(exit, operationErr)
	if err := execution.Finish(exit, operationErr); err != nil {
		w.failure(err)
		exit = sandbox.CommandTerminalExit{ExitCode: -1, Reason: "unknown"}
	}
	w.mu.Lock()
	if w.active == command {
		w.active = nil
	}
	w.mu.Unlock()
	w.json(
		map[string]any{
			"type":         "exit",
			"execution_id": execution.ExecutionID,
			"exit_code":    exit.ExitCode,
			"reason":       exit.Reason,
		},
	)
}
