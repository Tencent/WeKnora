package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cubeMockServer emulates the subset of Cube endpoints the Go SDK talks to on
// behalf of WeKnora:
//
//	CubeAPI:   POST /sandboxes,
//	           GET  /sandboxes,
//	           GET  /sandboxes/{id},
//	           DELETE /sandboxes/{id},
//	           POST /sandboxes/{id}/connect,
//	           POST /sandboxes/{id}/pause,
//	           GET  /health
//	CubeProxy: POST /files (envd, host-header routed),
//	           POST /process.Process/Start (envd Connect-RPC stream)
//
// Both routes are served from a single httptest.Server so tests don't have to
// juggle two ports. Which one a request lands on is decided by the URL path
// (and, for envd, by the Host header), mirroring how the production SDK picks
// between control-plane and data-plane addresses.
type cubeMockServer struct {
	server        *httptest.Server
	sandboxDomain string

	mu         sync.Mutex
	sandboxes  map[string]bool // sandboxID -> alive?
	files      map[string]map[string][]byte
	execScript func(sandboxID, cmd string, args []string) (stdout, stderr string, exitCode int)

	createCount atomic.Int32
	killCount   atomic.Int32
	execCount   atomic.Int32
}

func newCubeMockServer(t *testing.T) *cubeMockServer {
	t.Helper()
	m := &cubeMockServer{
		sandboxDomain: "cube.app",
		sandboxes:     map[string]bool{},
		files:         map[string]map[string][]byte{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.server.Close)
	return m
}

// SetExecutor lets each test control the fake process output.
// The callback receives the command line and returns stdout/stderr/exit code.
func (m *cubeMockServer) SetExecutor(fn func(sandboxID, cmd string, args []string) (string, string, int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execScript = fn
}

func (m *cubeMockServer) URL() string { return m.server.URL }

func (m *cubeMockServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})

	case r.URL.Path == "/sandboxes" && r.Method == http.MethodPost:
		m.handleCreate(w, r)

	case r.URL.Path == "/sandboxes" && r.Method == http.MethodGet:
		m.handleList(w)

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && strings.HasSuffix(r.URL.Path, "/connect") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sandboxes/"), "/connect")
		m.mu.Lock()
		alive := m.sandboxes[id]
		m.mu.Unlock()
		if !alive {
			// SDK expects to be able to reconnect to any sandbox it "knows"
			// about; the mock materialises it on demand so tests that hand-
			// craft a SandboxInfo without going through Create still work.
			m.mu.Lock()
			m.sandboxes[id] = true
			if _, ok := m.files[id]; !ok {
				m.files[id] = map[string][]byte{}
			}
			m.mu.Unlock()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"templateID":  "tpl-test",
			"sandboxID":   id,
			"clientID":    "test-client",
			"envdVersion": "test",
			"domain":      m.sandboxDomain,
		})

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && strings.HasSuffix(r.URL.Path, "/pause") && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusNoContent)

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		m.mu.Lock()
		alive := m.sandboxes[id]
		m.mu.Unlock()
		if !alive {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"templateID":  "tpl-test",
			"sandboxID":   id,
			"clientID":    "test-client",
			"envdVersion": "test",
			"domain":      m.sandboxDomain,
			"startedAt":   time.Now().UTC().Format(time.RFC3339),
			"state":       "running",
		})

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		m.killCount.Add(1)
		m.mu.Lock()
		delete(m.sandboxes, id)
		delete(m.files, id)
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case r.URL.Path == "/files" && r.Method == http.MethodPost:
		m.handleFileWrite(w, r)

	case r.URL.Path == "/process.Process/Start" && r.Method == http.MethodPost:
		m.handleProcessStart(w, r)

	default:
		http.NotFound(w, r)
	}
}

func (m *cubeMockServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	m.createCount.Add(1)
	var req struct {
		TemplateID string `json:"templateID"`
		Timeout    int    `json:"timeout"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	id := fmt.Sprintf("sbx-%d", time.Now().UnixNano())
	m.mu.Lock()
	m.sandboxes[id] = true
	m.files[id] = map[string][]byte{}
	m.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"templateID":  req.TemplateID,
		"sandboxID":   id,
		"clientID":    "test-client",
		"envdVersion": "test",
		"domain":      m.sandboxDomain,
	})
}

func (m *cubeMockServer) handleList(w http.ResponseWriter) {
	m.mu.Lock()
	items := make([]map[string]any, 0, len(m.sandboxes))
	for id := range m.sandboxes {
		items = append(items, map[string]any{
			"templateID":  "tpl-test",
			"sandboxID":   id,
			"clientID":    "test-client",
			"envdVersion": "test",
			"domain":      m.sandboxDomain,
			"startedAt":   time.Now().UTC().Format(time.RFC3339),
			"state":       "running",
		})
	}
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, items)
}

func (m *cubeMockServer) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := m.sandboxFromHost(r.Host)
	if !ok {
		http.Error(w, "unknown sandbox", http.StatusBadGateway)
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	if _, ok := m.files[sandboxID]; !ok {
		m.files[sandboxID] = map[string][]byte{}
	}
	m.files[sandboxID][p] = body
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, []map[string]string{{"name": filepath.Base(p), "path": p, "type": "file"}})
}

// handleProcessStart reads the WeKnora client's Connect-framed request body
// (1 flag byte + 4 big-endian length bytes + JSON — the same framing envd
// requires and that cubeClient.startProcess now produces) and streams back
// the three-event sequence that envd would normally produce (start / data /
// end), terminated by a trailer frame with the 0b10 flag bit set.
func (m *cubeMockServer) handleProcessStart(w http.ResponseWriter, r *http.Request) {
	m.execCount.Add(1)
	sandboxID, ok := m.sandboxFromHost(r.Host)
	if !ok {
		http.Error(w, "unknown sandbox", http.StatusBadGateway)
		return
	}

	// The request is a single Connect envelope; unwrap it before decoding the
	// JSON body. This mirrors what the real envd server does and guards
	// against a regression that drops the request framing.
	framed, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	payload, err := unframeConnectEnvelope(framed)
	if err != nil {
		http.Error(w, "protocol error: "+err.Error(), http.StatusBadRequest)
		return
	}
	var parsed struct {
		Process struct {
			Cmd  string   `json:"cmd"`
			Args []string `json:"args"`
			Envs map[string]string
			Cwd  string
		} `json:"process"`
	}
	_ = json.Unmarshal(payload, &parsed)

	m.mu.Lock()
	exec := m.execScript
	m.mu.Unlock()
	stdout, stderr, exitCode := "", "", 0
	if exec != nil {
		stdout, stderr, exitCode = exec(sandboxID, parsed.Process.Cmd, parsed.Process.Args)
	}

	w.Header().Set("Content-Type", "application/connect+json")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	writeFrame := func(flags byte, obj any) {
		buf, _ := json.Marshal(obj)
		envelope := make([]byte, 0, 5+len(buf))
		envelope = append(envelope, flags)
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(buf)))
		envelope = append(envelope, length[:]...)
		envelope = append(envelope, buf...)
		_, _ = w.Write(envelope)
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeFrame(0, map[string]any{"event": map[string]any{"start": map[string]any{"pid": 42}}})
	if stdout != "" {
		writeFrame(0, map[string]any{"event": map[string]any{"data": map[string]any{"stdout": b64(stdout)}}})
	}
	if stderr != "" {
		writeFrame(0, map[string]any{"event": map[string]any{"data": map[string]any{"stderr": b64(stderr)}}})
	}
	writeFrame(0, map[string]any{"event": map[string]any{"end": map[string]any{"exitCode": exitCode, "exited": true, "status": fmt.Sprintf("exit status %d", exitCode)}}})
	writeFrame(0x02, map[string]any{})
}

// sandboxFromHost pulls the sandbox ID out of the CubeProxy-style Host header
// "<port>-<sandbox_id>.<domain>". Returns ok=false when the header is not in
// that shape (defensive: helps tests with malformed clients fail loudly).
func (m *cubeMockServer) sandboxFromHost(host string) (string, bool) {
	// The port stripping is best-effort — httptest can inject an authority.
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if !strings.HasSuffix(host, "."+m.sandboxDomain) {
		return "", false
	}
	prefix := strings.TrimSuffix(host, "."+m.sandboxDomain)
	dash := strings.Index(prefix, "-")
	if dash < 0 {
		return "", false
	}
	return prefix[dash+1:], true
}

// -----------------------------------------------------------------------------
// helpers

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// unframeConnectEnvelope strips a single Connect envelope
// ([flag][uint32 length][payload]) and returns the payload. It's the mock's
// counterpart to cubeClient.encodeConnectEnvelope, so the test exercises the
// exact request framing the live envd expects.
func unframeConnectEnvelope(framed []byte) ([]byte, error) {
	if len(framed) < 5 {
		return nil, fmt.Errorf("short envelope: %d bytes", len(framed))
	}
	size := binary.BigEndian.Uint32(framed[1:5])
	if int(size) != len(framed)-5 {
		return nil, fmt.Errorf("promised %d bytes, got %d", size, len(framed)-5)
	}
	return framed[5 : 5+size], nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// -----------------------------------------------------------------------------
// actual tests

func testConfig(t *testing.T, mock *cubeMockServer) *Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeCube
	cfg.FallbackEnabled = false
	cfg.CubeAPIURL = mock.URL()
	cfg.CubeProxyURL = mock.URL()
	cfg.CubeSandboxDomain = mock.sandboxDomain
	cfg.CubeTemplate = "tpl-test"
	cfg.CubeIdleTTL = 200 * time.Millisecond
	cfg.CubeHTTPTimeout = 5 * time.Second
	cfg.DefaultTimeout = 5 * time.Second
	return cfg
}

// stubScript writes a throwaway file so ExecuteConfig has a real path to open.
// The file content matters only to the ScriptValidator; the sandbox mock
// echoes fake stdout regardless.
func stubScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.py")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return p
}

func TestCubeClient_Health(t *testing.T) {
	mock := newCubeMockServer(t)
	client := newCubeClient(testConfig(t, mock))
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health failed: %v", err)
	}
}

func TestCubeSandbox_ExecuteEphemeral_Success(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.SetExecutor(func(_, cmd string, args []string) (string, string, int) {
		return fmt.Sprintf("ran %s %v\n", cmd, args), "", 0
	})
	sb := NewCubeSandbox(testConfig(t, mock))

	script := stubScript(t, "print('hi')\n")
	result, err := sb.Execute(context.Background(), &ExecuteConfig{
		Script:         script,
		SkipValidation: true,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q err=%q stdout=%q)", result.ExitCode, result.Stderr, result.Error, result.Stdout)
	}
	// The SDK wraps the actual interpreter call inside `/bin/bash -l -c`, so
	// the outer cmd we see in the mock is /bin/bash. We only care that the
	// intended interpreter ("python3") did show up somewhere in the joined
	// command line — this survives future SDK refactors of the wrapper shape.
	if !strings.Contains(result.Stdout, "python3") {
		t.Fatalf("stdout does not mention python3: %q", result.Stdout)
	}
	// Ephemeral => sandbox must be killed exactly once.
	if got := mock.killCount.Load(); got != 1 {
		t.Fatalf("expected 1 kill, got %d", got)
	}
	if got := mock.createCount.Load(); got != 1 {
		t.Fatalf("expected 1 create, got %d", got)
	}
}

func TestSessionBoundManager_ReusesSandboxAcrossExecutes(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.SetExecutor(func(_, _ string, _ []string) (string, string, int) {
		return "ok\n", "", 0
	})
	cfg := testConfig(t, mock)
	cfg.CubeIdleTTL = time.Minute // avoid reaper races in test
	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })

	script := stubScript(t, "print('hi')\n")

	for i := 0; i < 3; i++ {
		result, err := mgr.Execute(context.Background(), &ExecuteConfig{
			Script:         script,
			SessionID:      "sess-A",
			SkipValidation: true,
		})
		if err != nil {
			t.Fatalf("iter %d Execute error: %v", i, err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("iter %d unexpected exit code %d", i, result.ExitCode)
		}
	}

	if got := mock.createCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 sandbox create for a bound session, got %d", got)
	}
	if got := mock.execCount.Load(); got != 3 {
		t.Fatalf("expected 3 executions, got %d", got)
	}
}

func TestSessionBoundManager_DifferentSessionsGetDifferentSandboxes(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.SetExecutor(func(_, _ string, _ []string) (string, string, int) { return "ok", "", 0 })
	cfg := testConfig(t, mock)
	cfg.CubeIdleTTL = time.Minute
	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })

	script := stubScript(t, "print('hi')\n")
	for _, sid := range []string{"sess-A", "sess-B", "sess-C"} {
		if _, err := mgr.Execute(context.Background(), &ExecuteConfig{
			Script:         script,
			SessionID:      sid,
			SkipValidation: true,
		}); err != nil {
			t.Fatalf("Execute %s: %v", sid, err)
		}
	}
	if got := mock.createCount.Load(); got != 3 {
		t.Fatalf("expected 3 distinct sandboxes, got %d", got)
	}
}

func TestSessionBoundManager_DestroySessionCleansUp(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.SetExecutor(func(_, _ string, _ []string) (string, string, int) { return "ok", "", 0 })
	cfg := testConfig(t, mock)
	cfg.CubeIdleTTL = time.Minute
	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })

	script := stubScript(t, "print('hi')\n")
	if _, err := mgr.Execute(context.Background(), &ExecuteConfig{
		Script:         script,
		SessionID:      "sess-A",
		SkipValidation: true,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Type-assertion path mirrors the one in sessionService.destroyBoundSandbox.
	destroyer, ok := any(mgr).(interface {
		DestroySession(context.Context, string) error
	})
	if !ok {
		t.Fatalf("SessionBoundManager should expose DestroySession")
	}
	if err := destroyer.DestroySession(context.Background(), "sess-A"); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	if got := mock.killCount.Load(); got != 1 {
		t.Fatalf("expected 1 kill after DestroySession, got %d", got)
	}
	// Deleting again is a no-op.
	if err := destroyer.DestroySession(context.Background(), "sess-A"); err != nil {
		t.Fatalf("DestroySession idempotent: %v", err)
	}
}

func TestSessionBoundManager_FallbackWhenCubeDown(t *testing.T) {
	// Point at an obviously-dead port to force health probe failure.
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeCube
	cfg.FallbackEnabled = true
	cfg.CubeAPIURL = "http://127.0.0.1:1"
	cfg.CubeProxyURL = "http://127.0.0.1:1"
	cfg.CubeSandboxDomain = "cube.app"
	cfg.CubeTemplate = "tpl"
	cfg.CubeHTTPTimeout = 200 * time.Millisecond

	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if mgr.GetType() != SandboxTypeLocal {
		t.Fatalf("expected fallback to local, got %s", mgr.GetType())
	}
}

func TestSessionBoundManager_EphemeralWhenNoSessionID(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.SetExecutor(func(_, _ string, _ []string) (string, string, int) { return "ok", "", 0 })
	cfg := testConfig(t, mock)
	cfg.CubeIdleTTL = time.Minute
	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })

	script := stubScript(t, "print('hi')\n")
	// No SessionID -> each call must create+kill its own sandbox.
	for i := 0; i < 2; i++ {
		if _, err := mgr.Execute(context.Background(), &ExecuteConfig{
			Script:         script,
			SkipValidation: true,
		}); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if got := mock.createCount.Load(); got != 2 {
		t.Fatalf("expected 2 creates (ephemeral), got %d", got)
	}
	if got := mock.killCount.Load(); got != 2 {
		t.Fatalf("expected 2 kills, got %d", got)
	}
}

// TestCubeClient_HostHeaderShape guards against regressions in the routing
// convention: CubeProxy needs Host = "<envdPort>-<sandboxID>.<domain>".
func TestCubeClient_HostHeaderShape(t *testing.T) {
	client := newCubeClient(&Config{
		CubeAPIURL:        "http://api",
		CubeProxyURL:      "http://proxy",
		CubeSandboxDomain: "cube.app",
		CubeEnvdPort:      DefaultCubeEnvdPort,
		CubeHTTPTimeout:   time.Second,
	})
	got := client.hostHeader(&SandboxInfo{ID: "abc", Domain: "cube.app"})
	want := fmt.Sprintf("%d-abc.cube.app", DefaultCubeEnvdPort)
	if got != want {
		t.Fatalf("hostHeader = %q, want %q", got, want)
	}
}

// TestCubeClient_UploadPreservesPath sanity-checks that the file upload URL
// contains the path query param — a regression here would silently upload to
// the wrong location in the sandbox filesystem. The mock plays both the
// /sandboxes/{id}/connect resurrection and the envd /files write, because
// the SDK re-attaches to any sandbox it wasn't itself the creator of.
func TestCubeClient_UploadPreservesPath(t *testing.T) {
	captured := make(chan url.Values, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sandboxes/abc/connect" && r.Method == http.MethodPost:
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID":  "tpl-test",
				"sandboxID":   "abc",
				"clientID":    "test-client",
				"envdVersion": "test",
				"domain":      "cube.app",
			})
		case r.URL.Path == "/files" && r.Method == http.MethodPost:
			captured <- r.URL.Query()
			writeJSON(w, http.StatusOK, []any{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := newCubeClient(&Config{
		CubeAPIURL:        srv.URL,
		CubeProxyURL:      srv.URL,
		CubeSandboxDomain: "cube.app",
		CubeEnvdPort:      DefaultCubeEnvdPort,
		CubeHTTPTimeout:   time.Second,
	})
	if err := client.WriteFile(context.Background(), &SandboxInfo{ID: "abc", Domain: "cube.app"}, "/tmp/foo.py", []byte("x=1")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	select {
	case q := <-captured:
		if q.Get("path") != "/tmp/foo.py" {
			t.Fatalf("path query = %q, want %q", q.Get("path"), "/tmp/foo.py")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("upload did not reach server")
	}
}
