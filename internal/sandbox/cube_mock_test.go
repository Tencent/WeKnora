package sandbox

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cubeMockServer emulates the Cube control-plane API and envd data-plane
// endpoints that the Cube SDK speaks to. CubeRemoteClient tests exercise the
// adapter through its public surface without requiring a real Cube deployment.
type cubeMockServer struct {
	server                  *httptest.Server
	mu                      sync.Mutex
	createBody              map[string]any
	createCount             atomic.Int32
	killCount               atomic.Int32
	nextID                  atomic.Int64
	sandboxes               map[string]map[string]any // sandboxID → sandbox state record
	snapshots               []cubeMockSnapshot
	snapshotSeq             int64
	snapshotPageSize        int
	snapshotStuckPagination bool // always return the same non-empty next token
	snapshotDeleteFailWith  int  // when set, DELETE /templates/:id returns this status
	snapshotCreateBody      map[string]any
	executor                func(sandboxID, cmd string, args []string) (stdout, stderr string, exitCode int)
	files                   map[string]map[string][]byte // sandboxID → path → content
	cmdHistory              []commandRecord
}

type cubeMockSnapshot struct {
	id        string
	sandboxID string
	names     []string
}

type commandRecord struct {
	sandboxID string
	cmd       string
	args      []string
}

func newCubeMockServer(t *testing.T) *cubeMockServer {
	t.Helper()
	m := &cubeMockServer{
		sandboxes: map[string]map[string]any{},
		files:     map[string]map[string][]byte{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.server.Close)
	return m
}

func (m *cubeMockServer) URL() string { return m.server.URL }

// SetExecutor installs a callback that is invoked when a command is executed
// inside any sandbox managed by this mock.
func (m *cubeMockServer) SetExecutor(f func(sandboxID, cmd string, args []string) (stdout, stderr string, exitCode int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executor = f
}

func (m *cubeMockServer) handle(w http.ResponseWriter, r *http.Request) {
	// envd data-plane requests are routed through the proxy with a Host
	// header of the form "49983-{sandboxID}.{domain}". Detect and handle
	// them before falling through to the control-plane routes.
	if isEnvdRequest(r.Host, "cube.app") {
		m.handleEnvd(w, r)
		return
	}

	switch {
	case r.URL.Path == "/sandboxes" && r.Method == http.MethodPost:
		m.handleCreate(w, r)
	case r.URL.Path == "/sandboxes" && r.Method == http.MethodGet:
		m.handleList(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && strings.HasSuffix(r.URL.Path, "/connect") && r.Method == http.MethodPost:
		m.handleConnect(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && strings.HasSuffix(r.URL.Path, "/snapshots") && r.Method == http.MethodPost:
		m.handleCreateSnapshot(w, r)
	case r.URL.Path == "/snapshots" && r.Method == http.MethodGet:
		m.handleListSnapshots(w, r)
	case strings.HasPrefix(r.URL.Path, "/templates/") && r.Method == http.MethodDelete:
		m.handleDeleteSnapshot(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodGet:
		m.handleGetInfo(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodDelete:
		m.handleDelete(w, r)
	case r.URL.Path == "/health" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
}

func (m *cubeMockServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	m.createCount.Add(1)
	body, _ := io.ReadAll(r.Body)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	m.mu.Lock()
	m.createBody = raw
	id := "cube-" + strconv.FormatInt(m.nextID.Add(1), 10)
	metadata := map[string]string{}
	if rawMeta, ok := raw["metadata"].(map[string]any); ok {
		for k, v := range rawMeta {
			metadata[k] = fmt.Sprint(v)
		}
	}
	m.sandboxes[id] = map[string]any{
		"sandboxID":   id,
		"templateID":  raw["templateID"],
		"clientID":    "client-" + id,
		"state":       "running",
		"envdVersion": "test",
		"metadata":    metadata,
		"startedAt":   time.Now().UTC().Format(time.RFC3339),
	}
	m.files[id] = map[string][]byte{}
	m.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"sandboxID":   id,
		"clientID":    "client-" + id,
		"envdVersion": "test",
		"domain":      "cube.app",
	})
}

func (m *cubeMockServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	id := extractSandboxID(r.URL.Path, "/connect")
	m.mu.Lock()
	_, ok := m.sandboxes[id]
	m.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "sandbox not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sandboxID":   id,
		"clientID":    "client-" + id,
		"envdVersion": "test",
		"domain":      "cube.app",
	})
}

func (m *cubeMockServer) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	id := extractSandboxID(r.URL.Path, "")
	m.mu.Lock()
	info, ok := m.sandboxes[id]
	m.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "sandbox not found"})
		return
	}
	data := cloneMap(info)
	data["sandboxID"] = id
	writeJSON(w, http.StatusOK, data)
}

func (m *cubeMockServer) handleList(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]map[string]any, 0, len(m.sandboxes))
	for id, info := range m.sandboxes {
		cp := cloneMap(info)
		cp["sandboxID"] = id
		items = append(items, cp)
	}
	writeJSON(w, http.StatusOK, items)
}

func (m *cubeMockServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := extractSandboxID(r.URL.Path, "")
	m.mu.Lock()
	_, ok := m.sandboxes[id]
	if ok {
		delete(m.sandboxes, id)
		delete(m.files, id)
	}
	m.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	m.killCount.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

func (m *cubeMockServer) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	id := extractSandboxID(r.URL.Path, "/snapshots")
	body, _ := io.ReadAll(r.Body)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sandboxes[id]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "sandbox not found"})
		return
	}
	m.snapshotCreateBody = raw
	m.snapshotSeq++
	snapshotID := "snap-" + strconv.FormatInt(m.snapshotSeq, 10)
	var names []string
	if rawName, ok := raw["name"].(string); ok {
		if name := strings.TrimSpace(rawName); name != "" {
			names = []string{name}
		}
	}
	m.snapshots = append(m.snapshots, cubeMockSnapshot{
		id:        snapshotID,
		sandboxID: id,
		names:     names,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"snapshotID": snapshotID,
		"names":      names,
	})
}

func (m *cubeMockServer) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.URL.Query().Get("sandboxID")
	start := 0
	if token := r.URL.Query().Get("nextToken"); token != "" {
		if parsed, err := strconv.Atoi(token); err == nil && parsed > 0 {
			start = parsed
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	pageSize := m.snapshotPageSize
	if pageSize <= 0 {
		pageSize = len(m.snapshots)
	}

	filtered := make([]cubeMockSnapshot, 0, len(m.snapshots))
	for _, snapshot := range m.snapshots {
		if sandboxID != "" && snapshot.sandboxID != sandboxID {
			continue
		}
		filtered = append(filtered, snapshot)
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	items := make([]map[string]any, 0, end-start)
	for _, snapshot := range filtered[start:end] {
		items = append(items, map[string]any{
			"snapshotID": snapshot.id,
			"names":      snapshot.names,
		})
	}
	if m.snapshotStuckPagination {
		w.Header().Set("x-next-token", "stuck")
	} else if end < len(filtered) {
		w.Header().Set("x-next-token", strconv.Itoa(end))
	}
	writeJSON(w, http.StatusOK, items)
}

func (m *cubeMockServer) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/templates/")

	m.mu.Lock()
	failWith := m.snapshotDeleteFailWith
	m.mu.Unlock()
	if failWith != 0 {
		writeJSON(w, failWith, map[string]string{"message": "internal error"})
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i, snapshot := range m.snapshots {
		if snapshot.id != id {
			continue
		}
		m.snapshots = append(m.snapshots[:i], m.snapshots[i+1:]...)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "template not found"})
}

// handleEnvd intercepts data-plane requests that the Cube SDK sends through
// the proxy. These carry a Host header like "49983-{sandboxID}.cube.app".
func (m *cubeMockServer) handleEnvd(w http.ResponseWriter, r *http.Request) {
	sandboxID := extractSandboxIDFromHost(r.Host, "cube.app")
	if sandboxID == "" {
		http.NotFound(w, r)
		return
	}

	switch {
	case strings.Contains(r.URL.Path, "/process.Process/Start"):
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		m.handleCommandExec(w, r, sandboxID)
	case strings.Contains(r.URL.Path, "/filesystem") && (strings.Contains(r.URL.Path, "Read") || strings.Contains(r.URL.Path, "Stat") || strings.Contains(r.URL.Path, "ListDir")):
		m.handleFileRead(w, r, sandboxID)
	case strings.Contains(r.URL.Path, "/filesystem") && strings.Contains(r.URL.Path, "MakeDir"):
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		writeJSON(w, http.StatusOK, map[string]any{"type": "directory"})
	case strings.Contains(r.URL.Path, "/files") || containsPath(r.URL.Path, "Write"):
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		m.handleFileWrite(w, r, sandboxID)
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

// handleCommandExec intercepts envd /process.Process/Start and dispatches to
// the mock's executor callback.
func (m *cubeMockServer) handleCommandExec(w http.ResponseWriter, r *http.Request, sandboxID string) {
	m.mu.Lock()
	exec := m.executor
	m.mu.Unlock()
	if exec == nil {
		writeEnvdCommandResult(w, "", "", 0)
		return
	}

	cmd := "/bin/bash"
	args := []string{"-l", "-c", "<shell-line>"}
	body, _ := io.ReadAll(r.Body)
	// The envd body is connect-framed: [1 byte flags][4 bytes big-endian len][JSON].
	if len(body) >= 5 {
		msgLen := binary.BigEndian.Uint32(body[1:5])
		if int(msgLen)+5 <= len(body) {
			jsonBytes := body[5 : 5+int(msgLen)]
			var req struct {
				Process struct {
					Cmd  string   `json:"cmd"`
					Args []string `json:"args"`
				} `json:"process"`
			}
			if json.Unmarshal(jsonBytes, &req) == nil && req.Process.Cmd != "" {
				cmd = req.Process.Cmd
				args = req.Process.Args
			}
		}
	}

	m.mu.Lock()
	m.cmdHistory = append(m.cmdHistory, commandRecord{
		sandboxID: sandboxID,
		cmd:       cmd,
		args:      args,
	})
	m.mu.Unlock()

	stdout, stderr, code := exec(sandboxID, cmd, args)
	writeEnvdCommandResult(w, stdout, stderr, code)
}

func (m *cubeMockServer) handleFileWrite(w http.ResponseWriter, r *http.Request, sandboxID string) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	// The Cube SDK sends WriteFile as POST /files?path=%2Fworkspace%2Fhello.txt
	// with the raw file content as the body (no connect framing).
	path := r.URL.Query().Get("path")
	if path == "" || !strings.HasPrefix(path, "/") {
		path = "/workspace/unknown"
	}

	m.mu.Lock()
	if m.files[sandboxID] == nil {
		m.files[sandboxID] = map[string][]byte{}
	}
	// Store only the raw file content, not the full HTTP body framing.
	m.files[sandboxID][path] = body
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{})
}

// extractFilePathFromBody strips connect framing from the body and parses
// the JSON content to extract the file path. The Cube SDK uses connect+json
// for filesystem RPC, so the body is: [1 byte flags][4 bytes big-endian len][JSON].
// The JSON contains a "path" field.
func extractFilePathFromBody(body []byte) string {
	// Strip connect frame envelope: 1 byte flags + 4 bytes big-endian length.
	if len(body) < 5 {
		return "/workspace/unknown"
	}
	msgLen := binary.BigEndian.Uint32(body[1:5])
	if int(msgLen)+5 > len(body) {
		return "/workspace/unknown"
	}
	jsonBytes := body[5 : 5+int(msgLen)]

	var req struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(jsonBytes, &req) == nil && strings.HasPrefix(req.Path, "/") {
		return req.Path
	}
	return "/workspace/unknown"
}

func (m *cubeMockServer) handleFileRead(w http.ResponseWriter, _ *http.Request, sandboxID string) {
	m.mu.Lock()
	files := m.files[sandboxID]
	m.mu.Unlock()
	if files == nil {
		writeJSON(w, http.StatusOK, map[string]any{"content": ""})
		return
	}
	// Return the content of the requested file.
	writeJSON(w, http.StatusOK, map[string]any{"content": ""})
}

// --- helpers ---

func cloneMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeEnvdCommandResult(w http.ResponseWriter, stdout, stderr string, exitCode int) {
	// The Cube SDK ProcessEvent format: {"event":{...}}
	// stdout/stderr in data events MUST be base64-encoded (SDK calls
	// base64.StdEncoding.DecodeString on them).
	// Stream must include: start event, optional data events, end event,
	// then an end-of-stream connect frame (flags=0x02).
	var body bytes.Buffer

	// start event
	startJSON, _ := json.Marshal(map[string]any{
		"event": map[string]any{
			"start": map[string]any{"pid": 1},
		},
	})
	writeConnectFrame(&body, startJSON)

	// data event (if there is output) — stdout/stderr must be base64-encoded
	if stdout != "" || stderr != "" {
		dataJSON, _ := json.Marshal(map[string]any{
			"event": map[string]any{
				"data": map[string]any{
					"stdout": base64Encode(stdout),
					"stderr": base64Encode(stderr),
				},
			},
		})
		writeConnectFrame(&body, dataJSON)
	}

	// end event
	endJSON, _ := json.Marshal(map[string]any{
		"event": map[string]any{
			"end": map[string]any{
				"exitCode": exitCode,
				"exited":   true,
			},
		},
	})
	writeConnectFrame(&body, endJSON)

	// End-of-stream frame (connectEndStreamFlag = 0x02, empty payload).
	var eosHeader [5]byte
	eosHeader[0] = 0x02 // end-stream flag
	body.Write(eosHeader[:])

	w.Header().Set("Content-Type", "application/connect+json")
	w.Header().Set("Trailer", "grpc-status")
	w.Header().Set("Trailer", "grpc-message")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
	w.Header().Set("grpc-status", "0")
	w.Header().Set("grpc-message", "")
}

// base64Encode returns base64-encoded string, or empty string for empty input.
func base64Encode(s string) string {
	if s == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// writeConnectFrame appends a connect-framed message to buf:
// [1 byte flags=0x00][4 bytes big-endian length][payload]
func writeConnectFrame(buf *bytes.Buffer, payload []byte) {
	var header [5]byte
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))
	buf.Write(header[:])
	buf.Write(payload)
}

func extractSandboxID(urlPath string, suffix string) string {
	path := urlPath
	if suffix != "" {
		path = strings.TrimSuffix(path, suffix)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "sandboxes" {
		return parts[1]
	}
	return ""
}

func extractSandboxIDFromHost(host string, domain string) string {
	// Strip port if present (Go HTTP request Host may include port).
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// Host format is "{port}-{sandboxID}.{domain}". Strip the port
	// prefix (everything before the first "-").
	idx := strings.Index(host, "-")
	if idx < 0 {
		return ""
	}
	host = host[idx+1:]
	// Strip the domain suffix.
	host = strings.TrimSuffix(host, "."+domain)
	return host
}

func isEnvdRequest(host, domain string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Contains(host, "-") && strings.HasSuffix(host, "."+domain)
}

func containsPath(urlPath, needle string) bool {
	return strings.Contains(urlPath, needle)
}

// testConfig builds a *Config wired against the mock server. Both the control
// plane (CubeAPIURL) and the data plane (CubeProxyURL) are pointed at the
// mock so every SDK-initiated request goes through the mock's net/http server.
func testConfig(t *testing.T, mock *cubeMockServer) *Config {
	t.Helper()
	return &Config{
		Type:              SandboxTypeCube,
		CubeAPIURL:        mock.URL(),
		CubeAPIKey:        "",
		CubeProxyURL:      mock.URL(),
		CubeTemplate:      "template-a",
		CubeSandboxDomain: "cube.app",
		CubeSandboxTTL:    30 * time.Minute,
		CubeHTTPTimeout:   30 * time.Second,
		DefaultTimeout:    60 * time.Second,
	}
}
