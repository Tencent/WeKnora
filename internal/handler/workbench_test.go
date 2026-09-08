package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type workbenchHandlerSessions struct {
	interfaces.SessionService
	db *gorm.DB
}

func (s workbenchHandlerSessions) GetOwnedSession(ctx context.Context, id string) (*types.Session, error) {
	tid, _ := types.TenantIDFromContext(ctx)
	uid, _ := types.UserIDFromContext(ctx)
	var row types.Session
	err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND user_id = ?", id, tid, uid).Take(&row).Error
	return &row, err
}

type workbenchHandlerPolicy struct{ disabled atomic.Bool }

func (p *workbenchHandlerPolicy) WorkspaceScriptsDisabled(context.Context, uint64) (bool, error) {
	return p.disabled.Load(), nil
}

type workbenchHandlerConfigs struct {
	repository.TenantSandboxConfigRepository
}

func (workbenchHandlerConfigs) GetByID(_ context.Context, tenant uint64, id string) (*types.TenantSandboxConfigEntity, error) {
	if tenant != 7 || id != "config" {
		return nil, nil
	}
	return &types.TenantSandboxConfigEntity{ID: id, TenantID: tenant, SandboxType: "docker"}, nil
}

type workbenchHandlerResolver struct {
	sandbox.TenantSandboxResolver
	manager *workbenchHandlerManager
}

func (r workbenchHandlerResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return r.manager, nil
}

type workbenchHandlerAudit struct {
	interfaces.AuditLogRepository
	mu   sync.Mutex
	rows []*types.AuditLog
	fail atomic.Bool
}

func (a *workbenchHandlerAudit) Create(_ context.Context, row *types.AuditLog) error {
	if a.fail.Load() {
		return errors.New("private audit error")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, row)
	return nil
}
func (a *workbenchHandlerAudit) count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.rows) }

type workbenchHandlerManager struct {
	sandbox.Manager
	terminals chan *workbenchPipeTerminal
	opened    atomic.Int32
	fileErr   error
	lastFile  sandbox.WorkbenchFileRequest
}

func (m *workbenchHandlerManager) GetType() sandbox.SandboxType                         { return sandbox.SandboxTypeDocker }
func (m *workbenchHandlerManager) EnsureWorkbenchSession(context.Context, string) error { return nil }
func (m *workbenchHandlerManager) WorkbenchFiles(_ context.Context, _ string, r sandbox.WorkbenchFileRequest) (*sandbox.WorkbenchFileResult, error) {
	m.lastFile = r
	if m.fileErr != nil {
		return nil, m.fileErr
	}
	return &sandbox.WorkbenchFileResult{Path: r.Path, Entries: []sandbox.WorkbenchFileEntry{}, Content: []byte("hello")}, nil
}
func (m *workbenchHandlerManager) OpenSessionTerminal(context.Context, string, sandbox.TerminalRequest) (sandbox.Terminal, error) {
	reader, writer := io.Pipe()
	terminal := &workbenchPipeTerminal{reader: reader, writer: writer, done: make(chan struct{}), resized: make(chan [2]uint16, 2)}
	m.opened.Add(1)
	m.terminals <- terminal
	go func() { _, _ = writer.Write([]byte("early output\n")) }()
	return terminal, nil
}

type workbenchPipeTerminal struct {
	reader  *io.PipeReader
	writer  *io.PipeWriter
	done    chan struct{}
	resized chan [2]uint16
	once    sync.Once
	exit    sandbox.TerminalExit
	closed  atomic.Bool
}

func (p *workbenchPipeTerminal) Read(b []byte) (int, error) { return p.reader.Read(b) }
func (p *workbenchPipeTerminal) Input(_ context.Context, b []byte) error {
	_, err := p.writer.Write(append([]byte("input:"), b...))
	return err
}
func (p *workbenchPipeTerminal) Resize(_ context.Context, cols, rows uint16) error {
	p.resized <- [2]uint16{cols, rows}
	return nil
}
func (p *workbenchPipeTerminal) finish(code int, reason string) {
	p.once.Do(func() {
		p.exit = sandbox.TerminalExit{ExitCode: code, Reason: reason}
		_ = p.writer.Close()
		close(p.done)
	})
}
func (p *workbenchPipeTerminal) Interrupt(context.Context) error {
	p.finish(130, "interrupted")
	return nil
}
func (p *workbenchPipeTerminal) Close() error {
	p.closed.Store(true)
	p.finish(-1, "closed")
	_ = p.reader.Close()
	return nil
}
func (p *workbenchPipeTerminal) Wait(ctx context.Context) (sandbox.TerminalExit, error) {
	select {
	case <-p.done:
		return p.exit, nil
	case <-ctx.Done():
		return sandbox.TerminalExit{}, ctx.Err()
	}
}

type workbenchHandlerFixture struct {
	h       *WorkbenchHandler
	db      *gorm.DB
	router  *gin.Engine
	server  *httptest.Server
	manager *workbenchHandlerManager
	audit   *workbenchHandlerAudit
	policy  *workbenchHandlerPolicy
	redis   *miniredis.Miniredis
	ctx     context.Context
}

func newWorkbenchHandlerFixture(t *testing.T) *workbenchHandlerFixture {
	t.Helper()
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_ENABLED", "true")
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_ORIGINS", "http://127.0.0.1:15173")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqldb, err := db.DB()
	require.NoError(t, err)
	sqldb.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqldb.Close() })
	for _, sql := range []string{
		"CREATE TABLE users(id text PRIMARY KEY, is_active boolean, deleted_at datetime)",
		"CREATE TABLE tenants(id integer PRIMARY KEY, status text, deleted_at datetime)",
		"CREATE TABLE tenant_members(user_id text, tenant_id integer, status text, deleted_at datetime)",
		"CREATE TABLE im_channel_sessions(session_id text)",
		"INSERT INTO users VALUES('alice', true, NULL)", "INSERT INTO tenants VALUES(7, 'active', NULL)",
		"INSERT INTO tenant_members VALUES('alice', 7, 'active', NULL)",
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	require.NoError(t, db.Model(&types.Session{}).Create(map[string]any{"id": "session", "tenant_id": 7, "user_id": "alice", "sandbox_config_id": "config"}).Error)
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	manager := &workbenchHandlerManager{terminals: make(chan *workbenchPipeTerminal, 8)}
	audit, policy := &workbenchHandlerAudit{}, &workbenchHandlerPolicy{}
	svc := service.NewWorkbenchService(service.WorkbenchServiceDeps{DB: db, Redis: client, Sessions: workbenchHandlerSessions{db: db}, Policy: policy, Pinner: service.NewSessionSandboxPinner(db), Resolver: workbenchHandlerResolver{manager: manager}, Configs: workbenchHandlerConfigs{}, Audit: audit})
	h, err := NewWorkbenchHandler(svc)
	require.NoError(t, err)
	h.recheckInterval = 50 * time.Millisecond
	ctx := (service.WorkbenchIdentity{TenantID: 7, UserID: "alice", SessionID: "session"}).Context(context.Background())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/sandbox-terminal", h.Terminal)
	r.Use(func(c *gin.Context) { c.Request = c.Request.WithContext(ctx); c.Next() })
	r.POST("/sessions/:id/sandbox/terminal-ticket", h.Ticket)
	r.POST("/sessions/:id/sandbox/files", h.Upload)
	r.GET("/sessions/:id/sandbox/files/download", h.Download)
	r.GET("/sessions/:id/sandbox/workbench", h.Status)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	return &workbenchHandlerFixture{h, db, r, server, manager, audit, policy, rdb, ctx}
}

func (f *workbenchHandlerFixture) ticket(t *testing.T) string {
	t.Helper()
	ticket, err := f.h.service.IssueTicket(f.ctx, "session", "http://127.0.0.1:15173")
	require.NoError(t, err)
	return ticket.Ticket
}
func (f *workbenchHandlerFixture) socket(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/sandbox-terminal", http.Header{"Origin": []string{"http://127.0.0.1:15173"}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func readWorkbenchEvent(t *testing.T, conn *websocket.Conn, expected string) map[string]any {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	kind, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, kind)
	var event map[string]any
	require.NoError(t, json.Unmarshal(raw, &event))
	require.Equal(t, expected, event["type"], string(raw))
	return event
}
func authenticateWorkbenchSocket(t *testing.T, conn *websocket.Conn, ticket string) {
	t.Helper()
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "auth", "ticket": ticket}))
	readWorkbenchEvent(t, conn, "ready")
}

func TestWorkbenchSocketStreamingInputResizeInterrupt(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "read text; echo $text"}))
	readWorkbenchEvent(t, conn, "started")
	kind, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, kind)
	require.Equal(t, "early output\n", string(raw))
	terminal := <-f.manager.terminals
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "second command"}))
	event := readWorkbenchEvent(t, conn, "error")
	require.Equal(t, "console_busy", event["code"])
	require.EqualValues(t, 1, f.manager.opened.Load())
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "stdin", "data": "hello\n"}))
	kind, raw, err = conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, kind)
	require.Equal(t, "input:hello\n", string(raw))
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "resize", "cols": 120, "rows": 30}))
	select {
	case dims := <-terminal.resized:
		require.Equal(t, [2]uint16{120, 30}, dims)
	case <-time.After(3 * time.Second):
		t.Fatal("resize not delivered")
	}
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "interrupt"}))
	event = readWorkbenchEvent(t, conn, "exit")
	require.EqualValues(t, 130, event["exit_code"])
	require.Equal(t, "interrupted", event["reason"])
	require.Eventually(t, func() bool { return terminal.closed.Load() && f.audit.count() == 2 }, time.Second, 10*time.Millisecond)
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "ping"}))
	readWorkbenchEvent(t, conn, "pong")
}

func TestWorkbenchSocketReplayLeaseAndDisconnect(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	ticket := f.ticket(t)
	first := f.socket(t)
	authenticateWorkbenchSocket(t, first, ticket)
	replay := f.socket(t)
	require.NoError(t, replay.WriteJSON(map[string]any{"type": "auth", "ticket": ticket}))
	require.Equal(t, "invalid_ticket", readWorkbenchEvent(t, replay, "error")["code"])
	second := f.socket(t)
	require.NoError(t, second.WriteJSON(map[string]any{"type": "auth", "ticket": f.ticket(t)}))
	require.Equal(t, "console_busy", readWorkbenchEvent(t, second, "error")["code"])
	require.NoError(t, first.WriteJSON(map[string]any{"type": "command", "command": "sleep 100"}))
	readWorkbenchEvent(t, first, "started")
	_, _, err := first.ReadMessage()
	require.NoError(t, err)
	terminal := <-f.manager.terminals
	require.NoError(t, first.Close())
	require.Eventually(t, func() bool { return terminal.closed.Load() && f.audit.count() == 2 }, 2*time.Second, 10*time.Millisecond)
}

func TestWorkbenchSocketPeriodicRevocation(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "sleep 100"}))
	readWorkbenchEvent(t, conn, "started")
	_, _, err := conn.ReadMessage()
	require.NoError(t, err)
	terminal := <-f.manager.terminals
	f.policy.disabled.Store(true)
	require.Eventually(t, func() bool { return terminal.closed.Load() && f.audit.count() == 2 }, 2*time.Second, 10*time.Millisecond)
}

func TestWorkbenchSocketAuditFailureNoLaunch(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	f.audit.fail.Store(true)
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "command", "command": "echo no"}))
	require.Equal(t, "audit_unavailable", readWorkbenchEvent(t, conn, "error")["code"])
	require.Zero(t, f.manager.opened.Load())
}

func TestWorkbenchSocketOriginAndFraming(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	for _, origin := range []string{"", "null", "https://evil.example", "http://127.0.0.1:15173.evil.example", "http://127.0.0.1:15173/path"} {
		_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/v1/sandbox-terminal", http.Header{"Origin": []string{origin}})
		require.Error(t, err)
		require.Equal(t, 403, response.StatusCode)
		_ = response.Body.Close()
	}
	for _, frame := range []struct {
		kind int
		data string
	}{{websocket.BinaryMessage, `{"type":"auth"}`}, {websocket.TextMessage, `{"type":"command","command":"echo no"}`}, {websocket.TextMessage, strings.Repeat("x", service.WorkbenchMaxFrameBytes+1)}} {
		conn := f.socket(t)
		require.NoError(t, conn.WriteMessage(frame.kind, []byte(frame.data)))
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, _, err := conn.ReadMessage()
		require.Error(t, err)
		_ = conn.Close()
	}
	conn := f.socket(t)
	authenticateWorkbenchSocket(t, conn, f.ticket(t))
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "resize", "cols": 501, "rows": 24}))
	require.Equal(t, "invalid_request", readWorkbenchEvent(t, conn, "error")["code"])
	require.Zero(t, f.manager.opened.Load())
}

func TestWorkbenchTicketHTTPOrigin(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	for _, test := range []struct {
		origin string
		status int
	}{{"http://127.0.0.1:15173", 200}, {"http://evil.example", 403}, {"", 200}} {
		r := httptest.NewRequest("POST", "/sessions/session/sandbox/terminal-ticket", nil)
		if test.origin != "" {
			r.Header.Set("Origin", test.origin)
		}
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, r)
		require.Equal(t, test.status, w.Code)
		if test.status == 200 {
			require.Contains(t, w.Body.String(), `"websocket_path":"/api/v1/sandbox-terminal"`)
		}
	}
}

func TestWorkbenchMultipartPathsAndDownload(t *testing.T) {
	f := newWorkbenchHandlerFixture(t)
	for _, test := range []struct {
		destination, filename string
		status                int
	}{{"nested/renamed.txt", "file.txt", 200}, {"../outside", "file.txt", 400}, {"ok.txt", "../bad.txt", 400}, {"ok.txt", "a\\b.txt", 400}, {"", "file.txt", 400}} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("path", test.destination))
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="file"; filename="`+strings.ReplaceAll(test.filename, `\`, `\\`)+`"`)
		part, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = io.WriteString(part, "payload")
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		r := httptest.NewRequest("POST", "/sessions/session/sandbox/files", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, r)
		require.Equal(t, test.status, w.Code, w.Body.String())
		if test.status == 200 {
			require.Equal(t, test.destination, f.manager.lastFile.Path)
		}
	}
	r := httptest.NewRequest("GET", "/sessions/session/sandbox/files/download?path=result.html", nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	require.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
}

func TestWorkbenchErrorNeverExposesProviderSecrets(t *testing.T) {
	status, code, message := workbenchError(errors.New("provider credential SECRET PID123 sandboxID456"))
	require.Equal(t, 503, status)
	require.Equal(t, "unavailable", code)
	require.NotContains(t, message, "SECRET")
	for _, test := range []struct {
		err    error
		status int
	}{{sandbox.ErrWorkbenchConflict, 409}, {sandbox.ErrWorkbenchNotFound, 404}, {sandbox.ErrWorkbenchPath, 400}, {sandbox.ErrWorkbenchTooLarge, 413}} {
		status, _, _ := workbenchError(test.err)
		require.Equal(t, test.status, status)
	}
}
