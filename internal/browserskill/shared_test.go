package browserskill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func sharedTestManager(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	binary := os.Getenv("BROWSERSKILL_TEST_BINARY")
	if binary == "" {
		t.Skip("set BROWSERSKILL_TEST_BINARY for native multi-user integration")
	}
	m := NewManager(testStore(t))
	m.binary = binary
	server := httptest.NewServer(m)
	m.publicURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/extension"
	t.Cleanup(server.Close)
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return m, ctx
}

type sharedFixture struct {
	ws    *websocket.Conn
	mu    sync.Mutex
	calls chan map[string]any
}

func TestAccountReportsConnectedExtensionVersion(t *testing.T) {
	m, ctx := sharedTestManager(t)
	scope := Scope{1, "extension-version"}
	fixture := connectSharedFixture(ctx, t, m, scope, "", "chrome")
	account, err := m.Account(ctx, scope)
	require.NoError(t, err)
	require.True(t, account.Connected)
	require.Equal(t, "0.3.0", account.ExtensionVersion)

	require.NoError(t, fixture.ws.Close())
	require.Eventually(t, func() bool { return !m.Status(scope, "").Connected }, time.Second, 10*time.Millisecond)
	account, err = m.Account(ctx, scope)
	require.NoError(t, err)
	require.NotNil(t, account.Device, "authorization survives a transient disconnection")
	require.Empty(t, account.ExtensionVersion, "do not display a stale version as the connected extension")
}

func TestConcurrentCommandsShareAutomaticSessionCreation(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "auto-start-queue"}
	connectSharedFixture(ctx, t, m, s, "", "browser")
	require.NoError(t, m.Control(ctx, s, "chat", "select"))
	begin, done := make(chan struct{}), make(chan error, 20)
	for range 20 {
		go func() {
			<-begin
			_, err := m.Call(ctx, s, "chat", "snapshot", nil)
			done <- err
		}()
	}
	close(begin)
	for range 20 {
		require.NoError(t, <-done)
	}
	require.NotEmpty(t, m.Status(s, "chat").SessionID)
}

func (f *sharedFixture) send(v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ws.WriteJSON(v)
}

func connectSharedFixture(
	ctx context.Context,
	t *testing.T,
	m *Manager,
	scope Scope,
	link, claimedID string,
	intercepts ...func(*sharedFixture, string, string, map[string]any) bool,
) *sharedFixture {
	t.Helper()
	var err error
	if link == "" {
		link, err = m.Pair(ctx, scope, "")
		require.NoError(t, err)
	}
	link = redeemTestPair(ctx, t, m, link)
	parts := strings.Split(link, "#")
	dialer := websocket.Dialer{Subprotocols: []string{AuthProtocol + parts[1]}}
	ws, _, err := dialer.DialContext(
		ctx,
		parts[0],
		http.Header{"Origin": []string{"chrome-extension://" + strings.Repeat("a", 32)}},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })
	require.NoError(
		t,
		ws.WriteJSON(map[string]any{"id": "handshake", "method": "system.handshake", "params": map[string]any{
			"client": "browser-skill-extension", "version": "0.3.0",
			"protocol_version": "1.3", "min_compatible_protocol": "1.0",
			"instance_id": claimedID, "label": scope.User,
			"browser": map[string]string{"name": "chrome", "version": "125"},
		}}),
	)
	var handshake map[string]any
	require.NoError(t, ws.ReadJSON(&handshake))
	require.Nil(t, handshake["error"])
	require.True(t, m.Status(scope, "chat").Connected)
	f := &sharedFixture{ws: ws, calls: make(chan map[string]any, 32)}
	go func() {
		for {
			var req struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if ws.ReadJSON(&req) != nil {
				return
			}
			if len(intercepts) > 0 && intercepts[0](f, req.ID, req.Method, req.Params) {
				continue
			}
			result := map[string]any{}
			switch req.Method {
			case "tool.session_start":
				if focused, ok := req.Params["focused"].(bool); !ok || focused {
					t.Errorf("session_start must reach extension with focused=false, got %v", req.Params["focused"])
				}
				result = map[string]any{"agent_window_id": 100}
			case "tool.session_stop":
				result = map[string]any{"returned_tab_ids": []int{}, "return_failures": []any{}}
			case "tool.snapshot":
				result = map[string]any{"text": scope.key(), "ref_count": 0, "tab_id": 1}
			case "tool.screenshot":
				f.calls <- req.Params
				result = map[string]any{
					"image_base64": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/" +
						"x8AAwMCAO+aTioAAAAASUVORK5CYII=",
					"format": "png", "width": 1, "height": 1, "tab_id": 1,
				}
			case "tool.request_help":
				f.calls <- req.Params
				result = map[string]any{"outcome": req.Params["prompt"], "tab_id": 1}
			case "tool.click":
				if f.send(map[string]any{"id": req.ID, "error": map[string]any{
					"code": "not_found", "message": "fixture ref missing",
					"data": map[string]any{"reason": "ref_not_found", "effect_state": "none", "tab_id": 1},
				}}) != nil {
					return
				}
				continue
			case "tool.navigate":
				f.calls <- req.Params
				if req.Params["url"] == "https://slow.example" {
					continue
				}
				result = map[string]any{"url": req.Params["url"], "tab_id": 1, "reached": "domcontentloaded"}
			case "cancel":
				if f.send(
					map[string]any{
						"id":    req.Params["rpc_id"],
						"error": map[string]string{"code": "cancelled", "message": "fixture cancelled"},
					},
				) != nil {
					return
				}
				result = map[string]any{"cancelled": true}
			case "ui.task_preview":
				result = map[string]any{"image_base64": "dGVzdA==", "format": "jpeg"}
			case "ui.task_focus":
				result = map[string]any{"focused": true}
			case "system.ping":
				result = map[string]any{"pong": true}
			}
			if f.send(map[string]any{"id": req.ID, "result": result}) != nil {
				return
			}
		}
	}()
	return f
}

func TestStopCancelsActiveCommandAndRejectsFurtherCalls(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "stop-user"}
	f := connectSharedFixture(ctx, t, m, s, "", "browser")
	require.NoError(t, m.Control(ctx, s, "chat", "start"))
	done := make(chan error, 1)
	go func() {
		_, err := m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://slow.example"})
		done <- err
	}()
	select {
	case <-f.calls:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, m.Control(ctx, s, "chat", "stop"))
	require.Error(t, <-done)
	require.False(t, m.Status(s, "chat").Selected)
	_, err := m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://must-not-run.example"})
	require.Error(t, err)
	require.NoError(t, m.Control(ctx, s, "chat", "stop"), "ending an ended task is idempotent")
}

func TestScreenshotUsesNativeTaskAndPreservesCrop(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "image-user"}
	f := connectSharedFixture(ctx, t, m, s, "", "browser")
	require.NoError(t, m.Control(ctx, s, "chat", "select"))
	result, err := m.Call(ctx, s, "chat", "screenshot",
		map[string]any{"ref": "e3", "tab_id": 1, "session_id": "foreign"})
	require.NoError(t, err)
	require.Contains(t, string(result), "image_base64")
	params := <-f.calls
	require.Equal(t, "e3", params["ref"])
	require.Equal(t, m.Status(s, "chat").SessionID, params["session_id"])
	require.Equal(t, "screenshot", m.Status(s, "chat").Action)

	_, err = m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://example.com"})
	require.NoError(t, err)
	<-f.calls
	status := m.Status(s, "chat")
	require.Equal(t, "navigate", status.Action)
	require.Equal(t, "https://example.com", status.PageURL)
	require.Empty(t, status.LastError)
}

func TestProgressReportsNavigationFailureAndFreezesElapsedTime(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "progress"}
	connectSharedFixture(ctx, t, m, s, "", "browser", func(
		f *sharedFixture, id, method string, _ map[string]any,
	) bool {
		if method != "tool.navigate" {
			return false
		}
		_ = f.send(map[string]any{"id": id, "result": map[string]any{
			"url": "https://user:pass@example.com/path?token=secret#fragment", "tab_id": 1,
			"reached": "timeout", "error_text": "document not ready",
		}})
		return true
	})
	require.NoError(t, m.Control(ctx, s, "chat", "select"))
	_, err := m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://example.com"})
	require.NoError(t, err, "the transport succeeded even though navigation did not finish")
	status := m.Status(s, "chat")
	require.Equal(t, "document not ready", status.LastError)
	require.Equal(t, "https://example.com/path", status.PageURL)
	d := m.get(s)
	d.mu.Lock()
	task := d.tasks["chat"]
	task.actionFinished = task.actionStarted.Add(1234 * time.Millisecond)
	d.mu.Unlock()
	require.EqualValues(t, 1234, m.Status(s, "chat").ActionElapsedMS)
}

func TestSharedDaemonRoutesAndIsolatesUsers(t *testing.T) {
	m, ctx := sharedTestManager(t)
	scopes := []Scope{{1, "alice"}, {1, "bob"}, {2, "alice"}}
	fixtures := make([]*sharedFixture, len(scopes))
	identities := map[string]bool{}
	for i, scope := range scopes {
		// Even deliberately colliding extension IDs must never replace another user.
		fixtures[i] = connectSharedFixture(ctx, t, m, scope, "", "same-untrusted-id")
		d := m.get(scope)
		require.Same(t, m.daemon, d.runtime)
		d.mu.Lock()
		identity := d.browserID
		d.mu.Unlock()
		require.NotEqual(t, "same-untrusted-id", identity)
		require.False(t, identities[identity])
		identities[identity] = true
		require.NoError(t, m.Control(ctx, scope, "chat", "start"))
	}
	errors := make(chan error, len(scopes))
	for _, scope := range scopes {
		go func(s Scope) {
			data, err := m.Call(ctx, s, "chat", "snapshot", nil)
			if err == nil && !strings.Contains(string(data), s.key()) {
				err = fmt.Errorf("wrong user's response: %s", data)
			}
			errors <- err
		}(scope)
	}
	for range scopes {
		require.NoError(t, <-errors)
	}
	alice, bob := scopes[0], scopes[1]
	aID, bID := m.Status(alice, "chat").SessionID, m.Status(bob, "chat").SessionID
	_, err := m.Call(
		ctx,
		alice,
		"chat",
		"navigate",
		map[string]any{"url": "https://example.com", "session_id": bID, "browser_instance_id": m.get(bob).browserID},
	)
	require.NoError(t, err)
	params := <-fixtures[0].calls
	require.Equal(t, aID, params["session_id"])
	require.NotContains(t, params, "browser_instance_id")
	select {
	case <-fixtures[1].calls:
		t.Fatal("Alice's operation reached Bob")
	default:
	}
	// An authenticated but hostile extension cannot stop or pause someone else's session.
	for _, event := range []string{"session.window_closed", "session.user_interrupt"} {
		require.NoError(
			t,
			fixtures[0].send(map[string]any{"event": event, "payload": map[string]any{"session_id": bID}}),
		)
	}
	// This response is ordered after the forged events on the same extension socket.
	_, err = m.Call(ctx, alice, "chat", "snapshot", nil)
	require.NoError(t, err)
	_, err = m.Call(ctx, bob, "chat", "snapshot", nil)
	require.NoError(t, err)
	require.False(t, m.Status(bob, "chat").Paused)

	pending := make(chan error, 1)
	go func() {
		_, err := m.Call(ctx, bob, "chat", "navigate", map[string]any{"url": "https://slow.example"})
		pending <- err
	}()
	select {
	case <-fixtures[1].calls:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	runtime := m.daemon
	if err := m.Revoke(ctx, alice); err != nil {
		t.Fatal(err)
	}
	require.False(t, runtime.exited(), "revoking a user killed the shared process")
	require.True(t, m.Status(bob, "chat").Connected)
	select {
	case err := <-pending:
		t.Fatalf("revoking Alice interrupted Bob: %v", err)
	default:
	}
	require.NoError(t, m.Control(ctx, bob, "chat", "pause"))
	select {
	case err := <-pending:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, m.Control(ctx, bob, "chat", "resume"))
	_, err = m.Call(ctx, bob, "chat", "snapshot", nil)
	require.NoError(t, err)

	// Re-pairing Alice allocates another identity without replacing Bob's connection.
	_ = connectSharedFixture(ctx, t, m, alice, "", m.get(bob).browserID)
	require.NoError(t, m.Control(ctx, alice, "chat", "start"))
	require.Same(t, runtime, m.get(alice).runtime)
	_, err = m.Call(ctx, bob, "chat", "snapshot", nil)
	require.NoError(t, err)
	// Rotate credentials on an existing device; only its own tasks are paused.
	link, err := m.Pair(ctx, alice, "")
	require.NoError(t, err)
	redeemTestPair(ctx, t, m, link)
	status, err := m.GetStatus(ctx, alice, "chat")
	require.NoError(t, err)
	require.True(t, status.Paused)
	require.False(t, m.Status(alice, "chat").Connected)
	_, err = m.Call(ctx, bob, "chat", "snapshot", nil)
	require.NoError(t, err)
	m.Close()
	require.True(t, runtime.exited())
	_, err = os.Stat(runtime.home)
	require.True(t, os.IsNotExist(err))
}

func TestConcurrentPairingSharesOneDaemon(t *testing.T) {
	m, ctx := sharedTestManager(t)
	const count = 12
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		// Reserved as in-progress extension handshakes, so none is idle.
		go func(i int) { _, err := m.acquireDevice(ctx, Scope{1, fmt.Sprint(i)}, true); results <- err }(i)
	}
	for i := 0; i < count; i++ {
		require.NoError(t, <-results)
	}
	require.Len(t, m.devices, count)
	for _, d := range m.devices {
		require.Same(t, m.daemon, d.runtime)
	}
	m.maxConnections = count
	_, err := m.ensureDevice(ctx, Scope{1, "over-limit"})
	require.ErrorIs(t, err, errCapacity)
	if err := m.Revoke(ctx, Scope{1, "0"}); err != nil {
		t.Fatal(err)
	}
	_, err = m.ensureDevice(ctx, Scope{1, "replacement"})
	require.NoError(t, err)
}

// Capacity counts live members, not every member this node has ever served.
func TestDisconnectedMembersReleaseCapacity(t *testing.T) {
	m, ctx := sharedTestManager(t)
	m.maxConnections = 2
	alice, bob, carol := Scope{1, "alice"}, Scope{1, "bob"}, Scope{1, "carol"}
	fixture := connectSharedFixture(ctx, t, m, alice, "", "alice-browser")
	require.NoError(t, m.Control(ctx, alice, "chat", "start"))
	d := m.get(alice)
	require.NoError(t, fixture.ws.Close())
	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.idleLocked()
	}, 5*time.Second, 20*time.Millisecond)

	// An unpaired selection is idle as well and must not pin a slot.
	require.NoError(t, m.Control(ctx, carol, "chat", "select"))
	connectSharedFixture(ctx, t, m, bob, "", "bob-browser")
	require.Nil(t, m.get(alice))
	require.Nil(t, m.get(carol))

	// Alice's interrupted task is durable and reloads paused on reconnection.
	status, err := m.GetStatus(ctx, alice, "chat")
	require.NoError(t, err)
	require.True(t, status.Selected)
	require.True(t, status.Paused)
	require.NoError(t, m.Control(ctx, alice, "chat", "pause"))
	connectSharedFixture(ctx, t, m, alice, "", "alice-browser")
	status = m.Status(alice, "chat")
	require.True(t, status.Connected)
	require.True(t, status.Selected)
	require.True(t, status.Paused)
}

// A handshake reserves its device; a failed lease claim must not leak it.
func TestFailedLeaseClaimDoesNotPinCapacity(t *testing.T) {
	m, ctx := sharedTestManager(t)
	m.maxConnections = 1
	alice := Scope{1, "alice"}
	link, err := m.Pair(ctx, alice, "")
	require.NoError(t, err)
	parts := strings.Split(redeemTestPair(ctx, t, m, link), "#")
	record, err := m.store.account(ctx, alice)
	require.NoError(t, err)
	require.NoError(t, m.store.claim(ctx, record, "other-node", "http://other:8080", randomID()))

	header := http.Header{"Origin": []string{"chrome-extension://" + strings.Repeat("a", 32)}}
	dialer := websocket.Dialer{Subprotocols: []string{AuthProtocol + parts[1]}}
	_, response, err := dialer.DialContext(ctx, parts[0], header)
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, response.StatusCode)
	_ = response.Body.Close()

	_, err = m.ensureDevice(ctx, Scope{1, "bob"})
	require.NoError(t, err, "the refused handshake must leave an evictable device")
	require.Nil(t, m.get(alice))
}

func TestSharedDaemonCrashKeepsAuthorizationAndPausesTasks(t *testing.T) {
	m, ctx := sharedTestManager(t)
	scope := Scope{1, "alice"}
	connectSharedFixture(ctx, t, m, scope, "", "old-browser")
	require.NoError(t, m.Control(ctx, scope, "chat", "start"))
	old := m.daemon
	require.NoError(t, old.cmd.Process.Kill())
	require.Eventually(t, func() bool { return !m.Status(scope, "chat").Connected }, 3*time.Second, 10*time.Millisecond)
	_, err := m.Call(ctx, scope, "chat", "snapshot", nil)
	require.Error(t, err)
	link, err := m.Pair(ctx, scope, "")
	require.NoError(t, err)
	connectSharedFixture(ctx, t, m, scope, link, "old-browser")
	require.NotSame(t, old, m.daemon)
	_, err = os.Stat(old.home)
	require.True(t, os.IsNotExist(err))
	require.Empty(t, m.Status(scope, "chat").SessionID)
	_, err = m.Call(ctx, scope, "chat", "snapshot", nil)
	require.Error(t, err, "recovery must not replay the old task")
	require.NoError(t, m.Control(ctx, scope, "chat", "start"))
	_, err = m.Call(ctx, scope, "chat", "snapshot", nil)
	require.NoError(t, err)
}

func TestSlowDaemonStartupDoesNotBlockStatusOrCancelledWaiter(t *testing.T) {
	m, ctx := sharedTestManager(t)
	gate := t.TempDir()
	t.Setenv("WEKNORA_BSK_START_GATE", gate)
	t.Setenv("WEKNORA_BSK_NATIVE", m.binary)
	wrapper := gate + "/delayed-bsk"
	require.NoError(t, os.WriteFile(wrapper, []byte(`#!/bin/sh
printf ready > "$WEKNORA_BSK_START_GATE/ready"
while [ ! -e "$WEKNORA_BSK_START_GATE/release" ]; do sleep 0.02; done
exec "$WEKNORA_BSK_NATIVE" "$@"
`), 0o700))
	m.binary = wrapper
	t.Cleanup(func() { _ = os.WriteFile(gate+"/release", nil, 0o600) })
	pending := make(chan error, 1)
	// Reserved like a pending extension handshake so Bob cannot evict it.
	go func() { _, err := m.acquireDevice(ctx, Scope{1, "alice"}, true); pending <- err }()
	require.Eventually(
		t,
		func() bool {
			select {
			case err := <-pending:
				t.Fatalf("daemon wrapper exited before opening startup gate: %v", err)
			default:
			}
			_, err := os.Stat(gate + "/ready")
			return err == nil
		},
		3*time.Second,
		10*time.Millisecond,
	)
	status := make(chan Status, 1)
	go func() { status <- m.Status(Scope{1, "bob"}, "chat") }()
	select {
	case s := <-status:
		require.False(t, s.Connected)
	case <-time.After(time.Second):
		t.Fatal("status waited behind process startup")
	}
	waiter, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := m.ensureDevice(waiter, Scope{1, "bob"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, os.WriteFile(gate+"/release", nil, 0o600))
	select {
	case err := <-pending:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = m.ensureDevice(ctx, Scope{1, "bob"})
	require.NoError(t, err)
	require.Same(t, m.get(Scope{1, "alice"}).runtime, m.get(Scope{1, "bob"}).runtime)
}

func TestLivePreviewBypassesUnfinishedAutomation(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "preview-user"}
	f := connectSharedFixture(ctx, t, m, s, "", "browser")
	defer func() { _ = f.ws.Close() }()
	require.NoError(t, m.Control(ctx, s, "chat", "select"))
	done := make(chan error, 1)
	go func() {
		_, err := m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://slow.example"})
		done <- err
	}()
	select {
	case <-f.calls:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	frame, err := m.Preview(ctx, s, "chat")
	require.NoError(t, err)
	require.Contains(t, string(frame), "image_base64")
	require.NoError(t, m.Control(ctx, s, "chat", "pause"))
	select {
	case err := <-done:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestWindowInterruptResumesSameTask(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%v", running), func(t *testing.T) {
			m, ctx := sharedTestManager(t)
			s := Scope{1, "interrupt-user"}
			f := connectSharedFixture(ctx, t, m, s, "", "browser")
			require.NoError(t, m.Control(ctx, s, "chat", "start"))
			id := m.Status(s, "chat").SessionID
			done := make(chan error, 1)
			if running {
				go func() {
					_, err := m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://slow.example"})
					done <- err
				}()
				select {
				case <-f.calls:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			require.NoError(t, f.send(map[string]any{
				"event": "session.user_interrupt", "payload": map[string]any{"session_id": id},
			}))
			require.Eventually(t, func() bool { return m.Status(s, "chat").Paused }, time.Second, time.Millisecond)
			if running {
				select {
				case err := <-done:
					require.Error(t, err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			require.True(t, m.Status(s, "chat").Connected)
			_, err := m.Call(ctx, s, "chat", "snapshot", nil)
			require.ErrorContains(t, err, "paused")
			require.NoError(t, m.Control(ctx, s, "chat", "resume"))
			require.Equal(t, id, m.Status(s, "chat").SessionID)
			_, err = m.Call(ctx, s, "chat", "navigate", map[string]any{"url": "https://continued.example"})
			require.NoError(t, err, "the first action after explicit resume must not consume a stale interrupt")
		})
	}
}

func TestHumanHelpOutcomeControlsPause(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "help-user"}
	f := connectSharedFixture(ctx, t, m, s, "", "browser")
	for _, outcome := range []string{"continued", "completed", "cancelled", "timed_out", "disabled"} {
		t.Run(outcome, func(t *testing.T) {
			require.NoError(t, m.Control(ctx, s, outcome, "start"))
			_, err := m.Call(ctx, s, outcome, "request_help", map[string]any{"prompt": outcome})
			require.NoError(t, err)
			select {
			case params := <-f.calls:
				require.Equal(t, float64(300000), params["timeout_ms"])
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			paused := outcome != "continued" && outcome != "completed"
			status := m.Status(s, outcome)
			require.Equal(t, paused, status.Paused)
			require.True(t, status.Connected)
			require.False(t, status.NeedsHelp)
			if paused {
				_, err = m.Call(ctx, s, outcome, "snapshot", nil)
				require.ErrorContains(t, err, "task_paused")
				require.NoError(t, m.Control(ctx, s, outcome, "resume"))
			}
			_, err = m.Call(ctx, s, outcome, "navigate", map[string]any{"url": "https://continued.example"})
			require.NoError(t, err)
			<-f.calls
		})
	}
}

func TestFinishTurnRetainsTaskAndCachedPreviewUntilNextCall(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "idle-user"}
	f := connectSharedFixture(ctx, t, m, s, "", "browser")
	defer func() { _ = f.ws.Close() }()
	require.NoError(t, m.Control(ctx, s, "chat", "start"))
	before := m.Status(s, "chat")
	frame, err := m.Preview(ctx, s, "chat")
	require.NoError(t, err)
	require.NoError(t, m.FinishTurn(ctx, s, "chat", true))
	after := m.Status(s, "chat")
	require.True(t, after.Idle)
	require.Equal(t, before.SessionID, after.SessionID)
	require.True(t, after.Selected)
	require.False(t, after.Paused)
	d := m.get(s)
	d.mu.Lock()
	d.tasks["chat"].previewAt = time.Time{}
	d.mu.Unlock()
	cached, err := m.Preview(ctx, s, "chat")
	require.NoError(t, err)
	require.Equal(t, frame, cached)
	_, err = m.Call(ctx, s, "chat", "snapshot", nil)
	require.NoError(t, err)
	require.False(t, m.Status(s, "chat").Idle)
	require.NoError(t, m.Control(ctx, s, "chat", "stop"))
	require.NoError(t, m.FinishTurn(ctx, s, "chat", true))
}

func TestNavigationDefaultsToDocumentReadyAndPreservesExplicitWait(t *testing.T) {
	m, ctx := sharedTestManager(t)
	scope := Scope{1, "navigation-user"}
	f := connectSharedFixture(ctx, t, m, scope, "", "browser")
	defer func() { _ = f.ws.Close() }()
	require.NoError(t, m.Control(ctx, scope, "chat", "select"))
	for _, wait := range []string{"", "load", "networkidle"} {
		params := map[string]any{"url": "https://example.com"}
		if wait != "" {
			params["wait_until"] = wait
		}
		_, err := m.Call(ctx, scope, "chat", "navigate", params)
		require.NoError(t, err)
		want := wait
		if want == "" {
			want = "domcontentloaded"
			require.NotContains(t, params, "wait_until")
		}
		require.Equal(t, want, (<-f.calls)["wait_until"])
	}
}

func TestFinishTurnClosesResearchButRetainsHandoffsAndPausedTasks(t *testing.T) {
	m, ctx := sharedTestManager(t)
	scope := Scope{1, "finish-user"}
	connectSharedFixture(ctx, t, m, scope, "", "browser")
	for _, tc := range []struct {
		name         string
		keep, paused bool
	}{
		{"research", false, false}, {"handoff", true, false}, {"paused", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, m.Control(ctx, scope, tc.name, "start"))
			id := m.Status(scope, tc.name).SessionID
			if tc.paused {
				require.NoError(t, m.Control(ctx, scope, tc.name, "pause"))
			}
			require.NoError(t, m.FinishTurn(ctx, scope, tc.name, tc.keep))
			status := m.Status(scope, tc.name)
			if tc.keep || tc.paused {
				require.Equal(t, id, status.SessionID)
				require.True(t, status.Idle)
				require.Equal(t, tc.paused, status.Paused)
			} else {
				require.Empty(t, status.SessionID)
				require.NoError(t, m.Control(ctx, scope, tc.name, "select"))
				_, err := m.Call(ctx, scope, tc.name, "snapshot", nil)
				require.NoError(t, err)
				require.NotEmpty(t, m.Status(scope, tc.name).SessionID)
			}
		})
	}
}

func TestFinishTurnUsesOfficialLifecycleWithoutGatewayCalls(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%t", remote), func(t *testing.T) {
			owner, ctx := sharedTestManager(t)
			caller := owner
			if remote {
				owner.clusterSecret = strings.Repeat("cluster-secret-", 3)
				server := httptest.NewServer(http.HandlerFunc(owner.InternalHTTP))
				defer server.Close()
				owner.internalURL = server.URL
				caller = NewManager(owner.store)
				caller.binary = owner.binary
				caller.clusterSecret = owner.clusterSecret
				t.Cleanup(caller.Close)
			}
			scope := Scope{1, "official-extension"}
			var gatewayCalls atomic.Int32
			connectSharedFixture(ctx, t, owner, scope, "", "browser", func(
				f *sharedFixture, id, method string, _ map[string]any,
			) bool {
				if !strings.HasPrefix(method, "ui.") {
					return false
				}
				gatewayCalls.Add(1)
				_ = f.send(map[string]any{"id": id, "error": map[string]string{"code": "unknown_method"}})
				return true
			})
			require.NoError(t, caller.Control(ctx, scope, "research", "start"))
			require.NoError(t, caller.FinishTurn(ctx, scope, "research", false))
			require.Empty(t, owner.Status(scope, "research").SessionID)
			for _, keep := range []bool{false, true} {
				session := fmt.Sprintf("retained-%t", keep)
				require.NoError(t, caller.Control(ctx, scope, session, "start"))
				if !keep {
					require.NoError(t, caller.Control(ctx, scope, session, "pause"))
				}
				id := owner.Status(scope, session).SessionID
				require.NoError(t, caller.FinishTurn(ctx, scope, session, keep))
				require.Equal(t, id, owner.Status(scope, session).SessionID)
				if keep {
					_, err := caller.Call(ctx, scope, session, "snapshot", nil)
					require.NoError(t, err)
					require.Equal(t, id, owner.Status(scope, session).SessionID)
				}
			}
			require.Zero(t, gatewayCalls.Load(), "turn cleanup must not send custom gateway RPC")
		})
	}
}

// Inspect the actual parameters forwarded by the native daemon, not just a
// timeout helper: tab_borrow ignores request_help's timeout_ms field.
func TestBorrowConfirmationBudgetReachesExtension(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "borrow-budget"}
	type pendingBorrow struct {
		fixture *sharedFixture
		id      string
		params  map[string]any
	}
	pending := make(chan pendingBorrow, 1)
	connectSharedFixture(ctx, t, m, s, "", "browser", func(
		f *sharedFixture, id, method string, params map[string]any,
	) bool {
		if method != "tool.tab_borrow" {
			return false
		}
		pending <- pendingBorrow{f, id, params}
		return true
	})
	require.NoError(t, m.Control(ctx, s, "chat", "start"))
	for _, tc := range []struct {
		name  string
		value any
		want  float64
	}{
		{"default", nil, 300000},
		{"explicit", 10000, 10000},
		{"bounded", 600000, 300000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"tab_id": 7}
			if tc.value != nil {
				params["confirmation_timeout_ms"] = tc.value
			}
			done := make(chan error, 1)
			go func() {
				_, err := m.Call(ctx, s, "chat", "tab_borrow", params)
				done <- err
			}()
			var request pendingBorrow
			select {
			case request = <-pending:
			case err := <-done:
				t.Fatalf("borrow returned before extension confirmation: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Equal(t, tc.want, request.params["confirmation_timeout_ms"])
			require.NotContains(t, request.params, "timeout_ms")
			require.True(t, m.Status(s, "chat").NeedsHelp)
			require.Equal(t, "tab_borrow", m.Status(s, "chat").Action)
			require.NoError(t, request.fixture.send(map[string]any{"id": request.id, "result": map[string]any{
				"tab_id": 7, "original_window_id": 200, "agent_window_id": 100,
			}}))
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.False(t, m.Status(s, "chat").NeedsHelp)
		})
	}
}

func TestAgentClosingLastTabKeepsAgentWindow(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "last-tab"}
	var mu sync.Mutex
	var methods []string
	var created []map[string]any
	var closeError string // error code the fixture returns when closing tab 9
	var closeDrops bool   // the extension closed the tab before failing the reply
	tabs := []map[string]any{{"tab_id": 7, "window_id": 100, "scope": "agent"}}
	removeTab := func(id any) {
		want, _ := numericID(id)
		tabs = slices.DeleteFunc(tabs, func(tab map[string]any) bool {
			got, _ := numericID(tab["tab_id"])
			return got == want
		})
	}
	connectSharedFixture(ctx, t, m, s, "", "browser", func(
		f *sharedFixture, id, method string, params map[string]any,
	) bool {
		mu.Lock()
		defer mu.Unlock()
		var result any
		switch method {
		case "tool.tab_list":
			if params["scope"] != "agent" {
				_ = f.send(map[string]any{"id": id, "error": map[string]string{
					"code": "invalid_params", "message": "fixture expects the agent scope",
				}})
				return true
			}
			result = map[string]any{"tabs": append([]map[string]any(nil), tabs...)}
		case "tool.tab_create":
			created = append(created, params)
			tabs = append(tabs, map[string]any{"tab_id": 8, "window_id": 100, "scope": "agent"})
			result = map[string]any{"tab_id": 8, "window_id": 100, "url": "about:blank"}
		case "tool.tab_close":
			methods = append(methods, fmt.Sprintf("tool.tab_close:%v", params["tab_id"]))
			if target, _ := numericID(params["tab_id"]); closeError != "" && target == 9 {
				if closeDrops {
					removeTab(params["tab_id"])
				}
				_ = f.send(map[string]any{"id": id, "error": map[string]string{
					"code": closeError, "message": "fixture refused",
				}})
				return true
			}
			removeTab(params["tab_id"])
			_ = f.send(map[string]any{"id": id, "result": map[string]any{"tab_id": params["tab_id"]}})
			return true
		default:
			return false
		}
		methods = append(methods, method)
		_ = f.send(map[string]any{"id": id, "result": result})
		return true
	})
	require.NoError(t, m.Control(ctx, s, "chat", "select"))
	// The final agent tab gets a blank replacement so Chrome keeps the window.
	_, err := m.Call(ctx, s, "chat", "tab_close", map[string]any{"tab_id": float64(7)})
	require.NoError(t, err)
	mu.Lock()
	require.Equal(t, []string{"tool.tab_list", "tool.tab_create", "tool.tab_close:7"}, methods)
	require.Len(t, created, 1)
	require.Equal(t, "about:blank", created[0]["url"])
	require.Equal(t, false, created[0]["active"])
	require.NotEmpty(t, created[0]["session_id"])
	methods = nil
	tabs = []map[string]any{
		{"tab_id": 8, "window_id": 100, "scope": "agent"},
		{"tab_id": 9, "window_id": 100, "scope": "user"},
	}
	mu.Unlock()
	// Any other tab in the Agent Window, even a user's, keeps it open.
	_, err = m.Call(ctx, s, "chat", "tab_close", map[string]any{"tab_id": 8})
	require.NoError(t, err)
	mu.Lock()
	require.Equal(t, []string{"tool.tab_list", "tool.tab_close:8"}, methods)
	// A refused close (unauthorized or borrowed tab) must not leave the
	// placeholder behind: the target is still there, so the blank tab goes.
	methods, created = nil, nil
	tabs = []map[string]any{{"tab_id": 9, "window_id": 100, "scope": "agent"}}
	closeError = "permission_denied"
	mu.Unlock()
	_, err = m.Call(ctx, s, "chat", "tab_close", map[string]any{"tab_id": 9})
	require.ErrorContains(t, err, "permission_denied")
	mu.Lock()
	require.Equal(t, []string{
		"tool.tab_list", "tool.tab_create", "tool.tab_close:9", "tool.tab_list", "tool.tab_close:8",
	}, methods)
	require.Len(t, tabs, 1)
	require.Equal(t, 9, tabs[0]["tab_id"])
	require.False(t, m.Status(s, "chat").Paused)
	// When the extension already removed the target before the reply failed,
	// the placeholder is what keeps the window open and must stay.
	methods, created = nil, nil
	closeError, closeDrops = "cdp_failed", true
	mu.Unlock()
	_, err = m.Call(ctx, s, "chat", "tab_close", map[string]any{"tab_id": 9})
	require.ErrorContains(t, err, "cdp_failed")
	mu.Lock()
	require.Equal(t, []string{
		"tool.tab_list", "tool.tab_create", "tool.tab_close:9", "tool.tab_list",
	}, methods)
	require.Len(t, tabs, 1)
	require.Equal(t, 8, tabs[0]["tab_id"])
	mu.Unlock()
}
