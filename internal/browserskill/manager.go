// Package browserskill connects authenticated users to an unmodified BrowserSkill
// daemon. Browser operations, tab consent and command scheduling remain upstream.
package browserskill

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// AuthProtocol identifies the credential-bearing WebSocket subprotocol.
const (
	AuthProtocol = "bsk-auth."
	maxFrame     = 8 << 20
)

// Scope isolates browser ownership by tenant and user.
type Scope struct {
	Tenant uint64
	User   string
}

func (s Scope) key() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", s.Tenant, s.User)))
	return hex.EncodeToString(sum[:16])
}
func (s Scope) valid() bool { return s.Tenant != 0 && s.User != "" }

// Status describes the connection and current conversation task.
type Status struct {
	Action          string `json:"action,omitempty"`
	ActionElapsedMS int64  `json:"action_elapsed_ms"`
	PageURL         string `json:"page_url,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	Stopping        bool   `json:"stopping"`
	HelpPrompt      string `json:"help_prompt,omitempty"`
	Idle            bool   `json:"idle"`
	NeedsHelp       bool   `json:"needs_help"`
	Enabled         bool   `json:"enabled"`
	Selected        bool   `json:"selected"`
	Connected       bool   `json:"connected"`
	Paused          bool   `json:"paused"`
	SessionID       string `json:"task_id,omitempty"`
}
type task struct {
	action                        string
	actionStarted, actionFinished time.Time
	pageURL, lastError            string
	lifecycleCancel               context.CancelFunc
	helpPrompt                    string
	commands                      chan struct{}
	helpCalls                     int
	previewAt                     time.Time
	previewData                   json.RawMessage
	previewBusy                   bool
	idle                          bool
	id                            string
	selected, paused              bool
	starting                      bool
	stopping                      bool
	forgotten                     bool
	epoch                         uint64
	calls                         map[uint64]context.CancelFunc
	nextCall                      uint64
}
type device struct {
	writeMu    sync.Mutex
	uiCalls    map[string]chan uiReply
	mu         sync.Mutex
	runtime    *daemon
	browserID  string
	upstream   *websocket.Conn
	conn       *websocket.Conn
	ready      bool
	generation uint64
	tasks      map[string]*task
	expires    time.Time
	recordID   string
}

// Manager owns transient browser connections; authorization and interruption
// markers are durable. Other replicas route commands to the lease owner.
type Manager struct {
	seenRPC                            map[string]time.Time
	mu                                 sync.Mutex
	binary, publicURL                  string
	daemon                             *daemon
	starting                           chan struct{}
	closed                             bool
	maxConnections                     int
	devices                            map[string]*device
	store                              *Store
	nodeID, internalURL, clusterSecret string
	forwardClient                      *http.Client
}

// NewManager configures the gateway from environment variables and a durable store.
func NewManager(stores ...*Store) *Manager {
	m := &Manager{
		binary:         os.Getenv("BROWSERSKILL_BINARY"),
		publicURL:      os.Getenv("BROWSERSKILL_PUBLIC_URL"),
		devices:        map[string]*device{},
		maxConnections: connectionLimit(),
		nodeID:         randomID(),
		internalURL:    os.Getenv("BROWSERSKILL_INTERNAL_URL"),
		clusterSecret:  os.Getenv("BROWSERSKILL_CLUSTER_SECRET"),
		forwardClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if len(stores) > 0 {
		m.store = stores[0]
	}
	return m
}

// Enabled reports whether the browser integration has been configured.
func (m *Manager) Enabled() bool { return m != nil && m.binary != "" }

func (m *Manager) get(s Scope) *device {
	if m == nil || !s.valid() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devices[s.key()]
}

// Status reads this node's transient task state without creating a task.
func (m *Manager) Status(s Scope, session string) Status {
	result := Status{Enabled: m.Enabled()}
	d := m.get(s)
	if d == nil {
		return result
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	result.Connected = d.conn != nil && d.ready && time.Now().Before(d.expires)
	if t := d.tasks[session]; t != nil {
		result.Selected = t.selected
		result.Paused = t.paused
		result.Action = t.action
		result.Stopping = t.stopping
		result.PageURL = t.pageURL
		result.LastError = t.lastError
		if !t.actionStarted.IsZero() {
			end := t.actionFinished
			if end.IsZero() {
				end = time.Now()
			}
			result.ActionElapsedMS = end.Sub(t.actionStarted).Milliseconds()
		}
		result.Idle = t.idle
		result.SessionID = t.id
		result.NeedsHelp = t.helpCalls > 0 && !t.paused
		if result.NeedsHelp {
			result.HelpPrompt = t.helpPrompt
		}
	}
	return result
}

func pairingEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("invalid BrowserSkill public URL")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "wss" && (!local || u.Scheme != "ws") {
		return "", errors.New("BrowserSkill public URL requires WSS")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("BrowserSkill public URL must not contain credentials, query or fragment")
	}
	return u.String(), nil
}

// pairingURL uses the explicit gateway override, or the browser's page origin.
// The origin only builds a link returned to the caller; it is never dialed by
// the server. Using the page origin preserves external ports behind proxies.
func (m *Manager) pairingURL(origin string) (string, error) {
	if m.publicURL != "" {
		return pairingEndpoint(m.publicURL)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("invalid BrowserSkill page origin")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", errors.New("BrowserSkill page origin requires HTTP or HTTPS")
	}
	u.Path = "/api/v1/local-browser/extension"
	return pairingEndpoint(u.String())
}

// Pair issues a five-minute, single-use activation link. Existing device
// authorization is replaced only when the extension redeems it successfully.
func (m *Manager) Pair(ctx context.Context, s Scope, origin string) (string, error) {
	if !m.Enabled() || !s.valid() || m.store == nil {
		return "", errors.New("local browser is unavailable")
	}
	endpoint, err := m.pairingURL(origin)
	if err != nil {
		return "", err
	}
	token := randomToken()
	err = m.store.createPair(
		ctx,
		PairingRecord{
			ScopeKey:  s.key(),
			TokenHash: tokenHash(token),
			Tenant:    s.Tenant,
			User:      s.User,
			ExpiresAt: time.Now().Add(pairingLifetime),
		},
	)
	if err != nil {
		return "", err
	}
	return endpoint + "#" + token, nil
}

// ensureDevice shares the daemon, but never shares authorization or task maps.
func (m *Manager) ensureDevice(ctx context.Context, s Scope) (*device, error) {
	runtime, err := m.ensureDaemon(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || runtime.exited() {
		return nil, errors.New("BrowserSkill daemon unavailable")
	}
	for key, old := range m.devices {
		old.mu.Lock()
		if old.runtime != runtime || (!old.expires.IsZero() && time.Now().After(old.expires)) {
			delete(m.devices, key)
			disconnectDeviceLocked(old)
			old.expires = time.Time{}
		}
		old.mu.Unlock()
	}
	d := m.devices[s.key()]
	if d == nil {
		if len(m.devices) >= m.maxConnections {
			return nil, errors.New("local browser connection capacity reached")
		}
		rows, err := m.store.tasks(ctx, s)
		if err != nil {
			return nil, err
		}
		d = &device{runtime: runtime, tasks: map[string]*task{}}
		for _, row := range rows {
			d.tasks[row.Session] = &task{selected: true, paused: true}
		}

		m.devices[s.key()] = d
	}
	return d, nil
}

// Caller holds d.mu. Never stops the shared daemon.
func disconnectDeviceLocked(d *device) {
	if d.conn != nil {
		_ = d.conn.Close()
	}
	if d.upstream != nil {
		_ = d.upstream.Close()
	}
	d.conn, d.upstream = nil, nil
	d.ready = false
	d.browserID = ""
	d.generation++
	for _, t := range d.tasks {
		t.id = ""
		t.previewData = nil
		pauseTask(t)
	}
}

// Revoke invalidates the member's durable grant and closes its local connection.
func (m *Manager) Revoke(ctx context.Context, s Scope) error {
	if m == nil || m.store == nil {
		return errors.New("local browser is unavailable")
	}
	if err := m.store.revoke(ctx, s); err != nil {
		return err
	}
	m.disconnectScope(s)
	return nil
}

func (m *Manager) disconnectScope(s Scope) {
	m.mu.Lock()
	d := m.devices[s.key()]
	delete(m.devices, s.key())
	m.mu.Unlock()
	if d != nil {
		d.mu.Lock()
		disconnectDeviceLocked(d)
		d.expires = time.Time{}
		d.mu.Unlock()
	}
}

// ServeHTTP authenticates each extension before dialing the shared daemon.
// Only the handshake identity is rewritten; browser execution stays upstream.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/authorize") {
		m.AuthorizeHTTP(w, r)
		return
	}
	if r.URL.Path == internalPath {
		m.InternalHTTP(w, r)
		return
	}
	if !m.Enabled() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	origin := r.Header.Get("Origin")
	if !validExtensionOrigin(origin) {
		http.Error(w, "invalid extension origin", http.StatusForbidden)
		return
	}
	protocols := websocket.Subprotocols(r)
	if len(protocols) != 1 || !strings.HasPrefix(protocols[0], AuthProtocol) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(protocols[0], AuthProtocol)
	if len(token) != 43 {
		http.Error(w, "invalid pairing", http.StatusUnauthorized)
		return
	}
	authCtx, authCancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer authCancel()
	if m.store == nil {
		http.Error(w, "authorization unavailable", http.StatusServiceUnavailable)
		return
	}
	record, err := m.store.authenticate(authCtx, tokenHash(token))
	if err != nil {
		http.Error(w, "device authorization invalid; pair again", http.StatusUnauthorized)
		return
	}
	d, err := m.ensureDevice(authCtx, record.scope())
	if err != nil {
		http.Error(w, "browser runtime unavailable", http.StatusServiceUnavailable)
		return
	}
	leaseKey := randomID()
	if err = m.store.claim(authCtx, record, m.nodeID, m.internalURL, leaseKey); err != nil {
		http.Error(w, "browser connection owned elsewhere; retry shortly", http.StatusConflict)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.store.release(ctx, record.ID, leaseKey)
	}()
	d.mu.Lock()
	if d.conn != nil {
		d.mu.Unlock()
		http.Error(w, "browser already connected", http.StatusConflict)
		return
	}
	d.recordID = record.ID
	d.expires = record.ExpiresAt
	// Hold the device lock across the bounded connect/upgrade to exclude a second connector.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	target := fmt.Sprintf("ws://127.0.0.1:%d", d.runtime.port)
	up, _, err := websocket.DefaultDialer.DialContext(ctx, target, http.Header{"Origin": []string{origin}})
	if err != nil {
		d.mu.Unlock()
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, Subprotocols: protocols}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		d.mu.Unlock()
		_ = up.Close()
		return
	}
	d.conn = conn
	d.upstream = up
	d.ready = false
	// The lease identity is assigned by the authenticated gateway, never the extension.
	d.browserID = leaseKey
	browserID := d.browserID
	d.generation++
	generation := d.generation
	d.mu.Unlock()
	defer func() {
		_ = conn.Close()
		_ = up.Close()
		d.mu.Lock()
		if d.generation == generation {
			disconnectDeviceLocked(d)
		}
		d.mu.Unlock()
	}()
	leaseCtx, leaseCancel := context.WithCancel(context.Background())
	defer leaseCancel()
	go m.watchLease(leaseCtx, d, record.ID, leaseKey, conn, up, generation)
	conn.SetReadLimit(maxFrame)
	up.SetReadLimit(maxFrame)
	// Complete and verify the first handshake before publishing readiness.
	// This keeps identity assignment out of the generic frame forwarding path.
	reply, err := relayHandshake(conn, up, browserID)
	if err != nil {
		return
	}
	d.mu.Lock()
	if d.generation != generation {
		d.mu.Unlock()
		return
	}
	d.ready = true
	d.mu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if conn.WriteMessage(websocket.TextMessage, reply) != nil {
		return
	}
	done := make(chan struct{}, 2)
	pump := func(dst, src *websocket.Conn, events bool) {
		defer func() { done <- struct{}{} }()
		for {
			typ, data, e := src.ReadMessage()
			if e != nil {
				return
			}
			if typ != websocket.TextMessage {
				return
			}
			if events {
				if m.receiveUI(d, data) {
					continue
				}
				if m.observe(d, data) {
					continue
				}
			}
			if !events {
				d.writeMu.Lock()
			}
			_ = dst.SetWriteDeadline(time.Now().Add(10 * time.Second))
			writeErr := dst.WriteMessage(typ, data)
			if !events {
				d.writeMu.Unlock()
			}
			if writeErr != nil {
				return
			}
		}
	}
	go pump(up, conn, true)
	go pump(conn, up, false)
	<-done
	_ = conn.Close()
	_ = up.Close()
	<-done
}

func relayHandshake(conn, up *websocket.Conn, browserID string) ([]byte, error) {
	deadline := time.Now().Add(5 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	_ = up.SetReadDeadline(deadline)
	_ = up.SetWriteDeadline(deadline)
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
		_ = up.SetReadDeadline(time.Time{})
	}()
	typ, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var frame map[string]json.RawMessage
	if typ != websocket.TextMessage || json.Unmarshal(data, &frame) != nil {
		return nil, errors.New("invalid browser handshake")
	}
	var method, id string
	var params map[string]json.RawMessage
	if json.Unmarshal(frame["method"], &method) != nil || method != "system.handshake" ||
		json.Unmarshal(frame["id"], &id) != nil || id == "" ||
		json.Unmarshal(frame["params"], &params) != nil || params == nil {
		return nil, errors.New("expected browser handshake")
	}
	params["instance_id"], _ = json.Marshal(browserID)
	frame["params"], _ = json.Marshal(params)
	if err := up.WriteJSON(frame); err != nil {
		return nil, err
	}
	typ, reply, err := up.ReadMessage()
	if err != nil {
		return nil, err
	}
	var response struct {
		ID     string `json:"id"`
		Result struct {
			Protocol string `json:"protocol_version"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if typ != websocket.TextMessage || json.Unmarshal(reply, &response) != nil ||
		response.ID != id || response.Result.Protocol == "" ||
		(len(response.Error) != 0 && string(response.Error) != "null") {
		return nil, errors.New("BrowserSkill handshake failed")
	}
	return reply, nil
}

func validExtensionOrigin(origin string) bool {
	const prefix = "chrome-extension://"
	if !strings.HasPrefix(origin, prefix) {
		return false
	}
	id := strings.TrimPrefix(origin, prefix)
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if c < 'a' || c > 'p' {
			return false
		}
	}
	return true
}

// observe consumes window interrupts at the gateway, which owns explicit pause
// and resume. Forwarding them as well leaves a second, one-shot daemon marker
// that passive resume snapshots cannot clear and rejects the next user-approved
// action. pauseTask cancels active RPCs through the normal daemon cancel path.
func (m *Manager) observe(d *device, data []byte) bool {
	var e struct {
		Event   string `json:"event"`
		Payload struct {
			Session string `json:"session_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(data, &e) != nil {
		return false
	}
	if e.Event != "session.user_interrupt" && e.Event != "session.window_closed" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.tasks {
		if t.id != "" && t.id == e.Payload.Session {
			if e.Event == "session.window_closed" && t.stopping {
				continue
			}
			pauseTask(t)
			if e.Event == "session.window_closed" {
				t.id = ""
			}
		}
	}
	return e.Event == "session.user_interrupt"
}

type rpcReply struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

func rpc(ctx context.Context, d *device, method string, params any) (json.RawMessage, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(d.runtime.home, "run", "daemon.sock"))
	if err != nil {
		return nil, errors.New("BrowserSkill daemon unavailable")
	}
	defer func() { _ = conn.Close() }()
	timeout := 35 * time.Second
	if IsHumanStep(strings.TrimPrefix(method, "tool.")) {
		timeout = HumanStepTimeout
	}
	deadline := time.Now().Add(timeout)
	if t, ok := ctx.Deadline(); ok {
		deadline = t
	}
	_ = conn.SetDeadline(deadline)
	bytes := make([]byte, 12)
	if _, err = rand.Read(bytes); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(bytes)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if err = json.NewEncoder(conn).Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxFrame)).ReadBytes('\n')
	if err != nil {
		if method != "cancel" {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = rpc(cancelCtx, d, "cancel", map[string]any{"rpc_id": id})
		}
		return nil, errors.New("browser command interrupted or timed out; do not replay actions automatically")
	}
	var reply rpcReply
	if json.Unmarshal(line, &reply) != nil || reply.ID != id {
		return nil, errors.New("invalid BrowserSkill response")
	}
	if reply.Error != nil {
		reply.Error.BoundDetails()
		return nil, reply.Error
	}
	return reply.Result, nil
}

var methods = map[string]bool{
	"snapshot":            true,
	"observe":             true,
	"navigate":            true,
	"navigate_back":       true,
	"navigate_forward":    true,
	"reload":              true,
	"click":               true,
	"fill":                true,
	"press":               true,
	"hover":               true,
	"wheel":               true,
	"scroll_to":           true,
	"select":              true,
	"tab_list":            true,
	"tab_create":          true,
	"tab_select":          true,
	"tab_close":           true,
	"tab_borrow":          true,
	"tab_return":          true,
	"get_html":            true,
	"evaluate":            true,
	"screenshot":          true,
	"focus":               true,
	"blur":                true,
	"console":             true,
	"network":             true,
	"wait_for_navigation": true,
	"wait_ms":             true,
	"window_resize":       true,
	"emulate":             true,
	"request_help":        true,
}

// Call serializes authorized automation commands within a conversation task.
func (m *Manager) Call(
	ctx context.Context,
	s Scope,
	session, method string,
	params map[string]any,
) (json.RawMessage, error) {
	if result, remote, err := m.route(ctx, s, session, "call", method, params); remote || err != nil {
		return result, err
	}
	if !methods[method] {
		return nil, errors.New("unsupported BrowserSkill tool")
	}
	d := m.get(s)
	if d == nil {
		return nil, errors.New("local browser is not paired")
	}
	// Serialize automation only; preview traffic has a separate UI channel.
	d.mu.Lock()
	target := d.tasks[session]
	if target == nil {
		d.mu.Unlock()
		return nil, errors.New("select a browser task first")
	}
	if target.commands == nil {
		target.commands = make(chan struct{}, 1)
	}
	gate := target.commands
	epoch := target.epoch
	d.mu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-gate }()
	d.mu.Lock()
	t := d.tasks[session]
	if t == nil || t != target || !t.selected {
		d.mu.Unlock()
		return nil, errors.New("local browser task ended; wait for a new user turn")
	}
	if d.conn == nil || !d.ready || time.Now().After(d.expires) {
		d.mu.Unlock()
		return nil, errors.New("local browser is offline; keep Chrome and the extension open for reconnection")
	}
	if t.paused || t.stopping || t.epoch != epoch {
		d.mu.Unlock()
		return nil, pausedError()
	}
	if t.id == "" {
		d.mu.Unlock()
		if err := m.Control(ctx, s, session, "auto_start"); err != nil {
			return nil, err
		}
		d.mu.Lock()
		// Recheck after session creation: pause, deletion or disconnect may have
		// happened while the extension was opening the task window.
		if d.tasks[session] != t || !t.selected || t.paused || t.id == "" || d.conn == nil || !d.ready ||
			time.Now().After(d.expires) {
			d.mu.Unlock()
			return nil, errors.New("local browser was interrupted; ask the user to resume")
		}
	}
	t.idle = false
	t.action, t.actionStarted, t.actionFinished, t.lastError = method, time.Now(), time.Time{}, ""
	if method == "tab_select" || method == "tab_close" || method == "tab_return" {
		t.pageURL = ""
	}
	id := t.id
	callCtx, cancel := context.WithCancel(ctx)
	t.nextCall++
	callID := t.nextCall
	if t.calls == nil {
		t.calls = map[uint64]context.CancelFunc{}
	}
	t.calls[callID] = cancel
	helping := IsHumanStep(method)
	if helping {
		t.helpCalls++
		t.helpPrompt, _ = params["prompt"].(string)
	}
	d.mu.Unlock()
	defer func() {
		cancel()
		d.mu.Lock()
		delete(t.calls, callID)
		if helping {
			t.helpCalls--
			t.helpPrompt = ""
		}
		d.mu.Unlock()
	}()
	clean := map[string]any{}
	for k, v := range params {
		if k != "session_id" && k != "browser_instance_id" {
			clean[k] = v
		}
	}
	// Reading page content does not require every image, ad and subframe to load.
	// Callers can explicitly request load/networkidle for a page that needs it.
	switch method {
	case "navigate", "navigate_back", "navigate_forward", "reload":
		if _, supplied := clean["wait_until"]; !supplied {
			clean["wait_until"] = "domcontentloaded"
		}
	}
	clean["session_id"] = id
	if helping {
		// Keep all transports inside the same bounded human-wait budget.
		clean["timeout_ms"] = humanTimeoutMS(clean["timeout_ms"])
	}
	result, err := rpc(callCtx, d, "tool."+method, clean)
	if method == "request_help" && err == nil {
		var help struct {
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal(result, &help) != nil || (help.Outcome != "continued" && help.Outcome != "completed") {
			d.mu.Lock()
			pauseTask(t)
			d.mu.Unlock()
		}
	}
	if sessionGone(err) {
		d.mu.Lock()
		if t.id == id {
			t.id = ""
			pauseTask(t)
		}
		d.mu.Unlock()
	}
	if interruptedCommand(err) {
		d.mu.Lock()
		pauseTask(t)
		d.mu.Unlock()
	}
	d.mu.Lock()
	t.actionFinished = time.Now()
	var page struct {
		URL      string          `json:"url"`
		FinalURL string          `json:"final_url"`
		Error    string          `json:"error_text"`
		OK       *bool           `json:"ok"`
		Failure  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(result, &page) == nil {
		if page.FinalURL != "" {
			t.pageURL = statusPageURL(page.FinalURL)
		} else if page.URL != "" {
			t.pageURL = statusPageURL(page.URL)
		}
	}
	if err != nil {
		t.lastError = err.Error()
	} else if NavigationIncomplete(method, result) {
		t.lastError = page.Error
		if t.lastError == "" {
			t.lastError = "Navigation did not reach the requested loading phase. " +
				"Observe the current page before continuing."
		}
	} else if method == "evaluate" && page.OK != nil && !*page.OK {
		var failure struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(page.Failure, &failure)
		t.lastError = failure.Text
	}
	if len(t.lastError) > 1200 {
		t.lastError = string([]rune(t.lastError)[:min(len([]rune(t.lastError)), 1200)])
	}
	d.mu.Unlock()
	return result, err
}

func interruptedCommand(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) &&
		(rpcErr.Code == "timeout" || rpcErr.Code == "cancelled" || rpcErr.Code == "user_aborted") {
		return true
	}
	return strings.Contains(err.Error(), "user_aborted") ||
		strings.Contains(err.Error(), "interrupted or timed out") ||
		strings.Contains(err.Error(), "unfinished command")
}

func sessionGone(err error) bool {
	return err != nil && err.Error() == "not_found: session not registered or already stopped"
}

func pauseTask(t *task) {
	t.paused = true
	t.epoch++
	for _, cancel := range t.calls {
		cancel()
	}
}

// Control selects, starts, pauses, resumes or ends a conversation task.
func (m *Manager) Control(ctx context.Context, s Scope, session, action string) error {
	if _, remote, err := m.route(ctx, s, session, "control", action, nil); remote || err != nil {
		return err
	}
	d := m.get(s)
	if d == nil && action == "select" && m.Enabled() && s.valid() {
		var err error
		d, err = m.ensureDevice(ctx, s)
		if err != nil {
			return err
		}
	}
	if d == nil && action == "finish" {
		return nil
	}
	if d == nil && action == "stop" && m.store != nil {
		return m.store.clearTask(ctx, s, session)
	}
	if d == nil {
		return errors.New("pair a browser first")
	}
	if action == "stop" {
		return m.stopTask(ctx, s, session, d, false)
	}
	d.mu.Lock()
	t := d.tasks[session]
	if action == "finish" {
		// Automatic cleanup must not resume or discard interrupted work, nor
		// interrupt a new command/start that raced with turn completion.
		if t == nil || t.paused || t.starting || t.stopping || len(t.calls) > 0 || !t.idle {
			d.mu.Unlock()
			return nil
		}
		d.mu.Unlock()
		return m.stopTask(ctx, s, session, d, true)
	}
	if action == "auto_start" && (t == nil || !t.selected || t.paused || t.forgotten) {
		d.mu.Unlock()
		return errors.New("local browser is not selected or is paused; ask the user to resume")
	}
	if t == nil {
		if len(d.tasks) >= 64 {
			d.mu.Unlock()
			return errors.New("end an existing browser task before starting another")
		}
		t = &task{}
		d.tasks[session] = t
	}
	if action == "pause" {
		pauseTask(t)
		d.mu.Unlock()
		return nil
	}
	if action != "start" && action != "resume" && action != "stop" && action != "select" && action != "auto_start" {
		d.mu.Unlock()
		return errors.New("invalid browser control")
	}
	if t.forgotten || t.stopping {
		d.mu.Unlock()
		return errors.New("browser conversation was deleted or its task is ending")
	}
	if action == "select" {
		// Re-selecting a tab never clears an interruption or resumes a task.
		t.selected = true
		d.mu.Unlock()
		return nil
	}
	if action == "auto_start" && t.id != "" {
		d.mu.Unlock()
		return nil
	}
	if t.starting {
		d.mu.Unlock()
		return errors.New("browser task lifecycle is busy")
	}
	if d.conn == nil || !d.ready || time.Now().After(d.expires) {
		d.mu.Unlock()
		return errors.New("browser is disconnected")
	}
	if len(t.calls) > 0 {
		d.mu.Unlock()
		return errors.New("pause and wait for the current operation before changing the task")
	}
	id := t.id
	generation := d.generation
	browserID := d.browserID
	t.starting = true
	t.selected = true
	if action == "auto_start" {
		// The first queued command creates the session on behalf of the whole
		// queue. Only a user interruption/resume invalidates queued commands.
		t.paused = true
	} else {
		pauseTask(t)
	}
	epoch := t.epoch
	lifecycleCtx, lifecycleCancel := context.WithCancel(ctx)
	t.lifecycleCancel = lifecycleCancel
	defer lifecycleCancel()
	d.mu.Unlock()
	method := "session.start"
	params := map[string]any{"focused": false, "browser_instance_id": browserID}
	if id != "" {
		// Refresh the observation before releasing a paused task to the agent.
		method = "tool.snapshot"
		params = map[string]any{"session_id": id}
	}
	// Persist before creating a window: a crash can never forget an active task.
	if err := m.store.markTask(ctx, s, session); err != nil {
		d.mu.Lock()
		t.starting = false
		t.lifecycleCancel = nil
		d.mu.Unlock()
		return err
	}
	result, err := rpc(lifecycleCtx, d, method, params)
	// A cancel acknowledgement can precede the extension's terminal response.
	// Only the passive resume snapshot may be retried while that response drains.
	drainUntil := time.Now().Add(2 * time.Second)
	for method == "tool.snapshot" && err != nil &&
		strings.Contains(err.Error(), "unfinished command") && time.Now().Before(drainUntil) {
		select {
		case <-lifecycleCtx.Done():
			err = lifecycleCtx.Err()
		case <-time.After(50 * time.Millisecond):
			result, err = rpc(lifecycleCtx, d, method, params)
		}
		if lifecycleCtx.Err() != nil {
			break
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	t.starting = false
	t.lifecycleCancel = nil
	if err != nil {
		if sessionGone(err) && t.id == id {
			t.id = ""
		}
		return err
	}
	if d.generation != generation {
		return errors.New("browser connection changed; start a new task explicitly")
	}
	if id == "" {
		var reply struct {
			ID        string `json:"session_id"`
			BrowserID string `json:"browser_instance_id"`
		}
		if json.Unmarshal(result, &reply) != nil || reply.ID == "" || reply.BrowserID != browserID {
			return errors.New("invalid task response")
		}
		t.id = reply.ID
	}
	if t.epoch == epoch {
		t.paused = false
		t.idle = false
	}
	return nil
}

// Preview is read-only and remains available while the user has paused actions.
func (m *Manager) Preview(ctx context.Context, s Scope, session string) (json.RawMessage, error) {
	if result, remote, err := m.route(ctx, s, session, "preview", "", nil); remote || err != nil {
		return result, err
	}
	d := m.get(s)
	if d == nil {
		return nil, errors.New("browser unavailable")
	}
	d.mu.Lock()
	t := d.tasks[session]
	if t == nil || t.id == "" || d.conn == nil || !d.ready || time.Now().After(d.expires) {
		d.mu.Unlock()
		return nil, errors.New("browser unavailable")
	}
	if len(t.previewData) > 0 && (t.idle || time.Since(t.previewAt) < 900*time.Millisecond) {
		cached := append(json.RawMessage(nil), t.previewData...)
		d.mu.Unlock()
		return cached, nil
	}
	if t.idle {
		d.mu.Unlock()
		return nil, errors.New("browser task is idle; no preview captured yet")
	}
	if t.previewBusy {
		d.mu.Unlock()
		return nil, errors.New("preview capture in progress")
	}
	t.previewBusy = true
	id := t.id
	d.mu.Unlock()
	defer func() { d.mu.Lock(); t.previewBusy = false; d.mu.Unlock() }()
	frame, err := m.callUI(ctx, s, session, "gateway.task_preview")
	if err != nil {
		return nil, err
	}
	var capture struct {
		Image  string `json:"image_base64"`
		Format string `json:"format"`
	}
	if json.Unmarshal(frame, &capture) != nil || capture.Image == "" || capture.Format != "jpeg" {
		return nil, errors.New("invalid preview frame")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if t.id != id || !d.ready || d.tasks[session] != t {
		return nil, errors.New("browser task changed during capture")
	}
	t.previewAt = time.Now()
	t.previewData = append(json.RawMessage(nil), frame...)
	return frame, nil
}

// ForgetAll prevents further agent calls when all of a member's conversations are deleted.
// BrowserSkill performs the actual tab-return/Agent Window cleanup.
func (m *Manager) ForgetAll(s Scope) {
	if m == nil || m.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := m.store.tasks(ctx, s)
	if err != nil {
		return
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Session)
	}
	m.Forget(s, ids)
}

// Forget stops and removes the specified deleted conversations' browser tasks.
func (m *Manager) Forget(s Scope, sessions []string) {
	if m == nil || m.store == nil {
		return
	}
	// Deletion can arrive at any app replica. Forward cleanup to the live owner.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, session := range sessions {
		if _, remote, err := m.route(ctx, s, session, "forget", "", nil); remote || err != nil {
			if err == nil {
				_ = m.store.clearTask(ctx, s, session)
			}
			continue
		}
		m.forgetLocal(s, []string{session})
	}
}

func (m *Manager) forgetLocal(s Scope, sessions []string) {
	d := m.get(s)
	if d == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, session := range sessions {
			_ = m.store.clearTask(ctx, s, session)
		}
		return
	}
	d.mu.Lock()
	forgotten := make(map[string]*task)
	for _, session := range sessions {
		if t := d.tasks[session]; t != nil {
			pauseTask(t)
			t.selected = false
			t.forgotten = true
			forgotten[session] = t
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = m.store.clearTask(ctx, s, session)
			cancel()
		}
	}
	d.mu.Unlock()
	for session, original := range forgotten {
		go func(session string, original *task) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// A session.start already in flight can still create a window. Wait
			// for its ID and cancellation cleanup before asking upstream to stop.
			var id string
			for {
				d.mu.Lock()
				if d.tasks[session] != original {
					d.mu.Unlock()
					return
				}
				busy := original.starting || len(original.calls) > 0
				id = original.id
				d.mu.Unlock()
				if !busy {
					break
				}
				select {
				case <-ctx.Done():
					return // Retain the tombstone; native session idle cleanup remains active.
				case <-time.After(50 * time.Millisecond):
				}
			}
			if id != "" {
				if _, err := rpc(ctx, d, "session.stop", map[string]any{"session_id": id, "all": false}); err != nil {
					return
				}
			}
			d.mu.Lock()
			if t := d.tasks[session]; t == original && t.id == id {
				if err := m.store.clearTask(ctx, s, session); err == nil {
					delete(d.tasks, session)
				}
			}
			d.mu.Unlock()
		}(session, original)
	}
}
