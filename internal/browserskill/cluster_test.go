package browserskill

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconnectAfterServerRestartKeepsAuthorizationAndPausesTasks(t *testing.T) {
	binary := os.Getenv("BROWSERSKILL_TEST_BINARY")
	if binary == "" {
		t.Skip("native daemon required")
	}
	store := testStore(t)
	m := NewManager(store)
	m.binary = binary
	var current atomic.Pointer[Manager]
	current.Store(m)
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { current.Load().ServeHTTP(w, r) }),
	)
	t.Cleanup(server.Close)
	t.Cleanup(func() { current.Load().Close() })
	m.publicURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/extension"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope := Scope{1, "alice"}
	link, err := m.Pair(ctx, scope, "")
	require.NoError(t, err)
	link = redeemTestPair(ctx, t, m, link)
	connectSharedFixture(ctx, t, m, scope, link, "chrome")
	require.NoError(t, m.Control(ctx, scope, "chat", "start"))
	oldID := m.Status(scope, "chat").SessionID
	m.Close()
	next := NewManager(store)
	next.binary = binary
	next.publicURL = m.publicURL
	current.Store(next)
	connectSharedFixture(ctx, t, next, scope, link, "chrome")
	status, err := next.GetStatus(ctx, scope, "chat")
	require.NoError(t, err)
	require.True(t, status.Connected)
	require.True(t, status.Paused)
	require.True(t, status.Selected)
	require.Empty(t, status.SessionID)
	require.NoError(t, next.Control(ctx, scope, "chat", "select"))
	_, err = next.Call(ctx, scope, "chat", "snapshot", nil)
	require.Error(t, err)
	require.NoError(t, next.Control(ctx, scope, "chat", "resume"))
	require.NotEqual(t, oldID, next.Status(scope, "chat").SessionID)
	_, err = next.Call(ctx, scope, "chat", "snapshot", nil)
	require.NoError(t, err)
	require.NoError(t, next.Control(ctx, scope, "chat", "stop"))
	rows, err := store.tasks(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestRequestsRouteToBrowserOwnerAcrossReplicas(t *testing.T) {
	owner, ctx := sharedTestManager(t)
	owner.clusterSecret = strings.Repeat("cluster-secret-", 3)
	server := httptest.NewServer(http.HandlerFunc(owner.InternalHTTP))
	defer server.Close()
	owner.internalURL = server.URL
	follower := NewManager(owner.store)
	follower.binary = owner.binary
	follower.publicURL = owner.publicURL
	follower.clusterSecret = owner.clusterSecret
	t.Cleanup(follower.Close)
	scope := Scope{1, "alice"}
	fixture := connectSharedFixture(ctx, t, owner, scope, "", "chrome")
	require.NoError(t, follower.Control(ctx, scope, "chat", "start"))
	require.Nil(t, follower.daemon, "routing must not launch a second daemon")
	status, err := follower.GetStatus(ctx, scope, "chat")
	require.NoError(t, err)
	require.True(t, status.Connected)
	_, err = follower.Call(ctx, scope, "chat", "navigate", map[string]any{"url": "https://example.com"})
	require.NoError(t, err)
	require.Equal(t, status.SessionID, (<-fixture.calls)["session_id"])
	_, err = follower.Call(ctx, scope, "chat", "click", map[string]any{"ref": "e99"})
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	require.EqualError(t, err, "not_found: fixture ref missing")
	require.JSONEq(t, `{"reason":"ref_not_found","effect_state":"none","tab_id":1}`, string(rpcErr.Data))
	require.NoError(t, follower.Control(ctx, scope, "chat", "pause"))
	require.True(t, owner.Status(scope, "chat").Paused)
	require.NoError(t, follower.Focus(ctx, scope, "chat"))
	require.True(t, owner.Status(scope, "chat").Paused, "locating a window must not resume automation")
	require.NoError(t, follower.Idle(ctx, scope, "chat"))
	require.True(t, owner.Status(scope, "chat").Idle)
	require.NoError(t, follower.Revoke(ctx, scope))
	_, err = owner.Call(ctx, scope, "chat", "snapshot", nil)
	require.ErrorIs(t, err, ErrAuthorization)
}

func TestInternalRPCRejectsForgeryAndReplay(t *testing.T) {
	m := NewManager(testStore(t))
	m.clusterSecret = strings.Repeat("test-key", 8)
	request := clusterRequest{Node: m.nodeID, Scope: Scope{1, "alice"}, Session: "chat", Operation: "status"}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), randomID()
	call := func(sig string) int {
		r := httptest.NewRequest(http.MethodPost, internalPath, bytes.NewReader(body))
		r.Header.Set("X-Browser-Timestamp", timestamp)
		r.Header.Set("X-Browser-Nonce", nonce)
		r.Header.Set("X-Browser-Signature", sig)
		w := httptest.NewRecorder()
		m.InternalHTTP(w, r)
		return w.Code
	}
	require.Equal(t, 401, call("forged"))
	sig := signRPC(m.clusterSecret, timestamp+"\n"+nonce, body)
	require.Equal(t, 200, call(sig))
	require.Equal(t, 409, call(sig))
}
