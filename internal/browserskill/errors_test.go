package browserskill

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRPCPreservesRecoveryDetails(t *testing.T) {
	// A short path also works with macOS's Unix socket path limit.
	home, err := os.MkdirTemp("", "bsk-rpc-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	require.NoError(t, os.Mkdir(filepath.Join(home, "run"), 0o700))
	listener, err := net.Listen("unix", filepath.Join(home, "run", "daemon.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		var request struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			done <- err
			return
		}
		done <- json.NewEncoder(conn).Encode(rpcReply{ID: request.ID, Error: &RPCError{
			Code: "permission_denied", Message: "not visible",
			Data: json.RawMessage(`{"reason":"element_not_visible","effect_state":"none","tab_id":42}`),
		}})
	}()
	_, err = rpc(context.Background(), &device{runtime: &daemon{home: home}}, "tool.click", map[string]any{"ref": "e1"})
	var rpcErr *RPCError
	require.ErrorAs(t, err, &rpcErr)
	require.EqualError(t, err, "permission_denied: not visible")
	require.JSONEq(t, `{"reason":"element_not_visible","effect_state":"none","tab_id":42}`, string(rpcErr.Data))
	require.NoError(t, <-done)
}

func TestRPCErrorBoundsLargeDetails(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"reason": "fill_failed", "effect_state": "unknown", "page": strings.Repeat("x", 9000),
	})
	require.NoError(t, err)
	rpcErr := &RPCError{Code: "cdp_failed", Message: "failure", Data: data}
	rpcErr.BoundDetails()
	require.JSONEq(t, `{"reason":"fill_failed","effect_state":"unknown","truncated":true}`, string(rpcErr.Data))
}

// Exercise the actual daemon parser, rather than assuming that a successful
// handshake means every advertised extension tool is supported by the binary.
func TestNativeDaemonSupportsAdvertisedMethods(t *testing.T) {
	binary := os.Getenv("BROWSERSKILL_TEST_BINARY")
	if binary == "" {
		t.Skip("set BROWSERSKILL_TEST_BINARY for native protocol coverage")
	}
	m := NewManager()
	m.binary = binary
	runtime, err := m.start(t.Context())
	require.NoError(t, err)
	t.Cleanup(runtime.stop)
	for method := range methods {
		t.Run(method, func(t *testing.T) {
			_, err := rpc(t.Context(), &device{runtime: runtime}, "tool."+method,
				map[string]any{"session_id": "missing-protocol-test-session"})
			if err == nil {
				return
			}
			var rpcErr *RPCError
			require.ErrorAs(t, err, &rpcErr)
			require.NotEqual(t, "daemon_incompatible", rpcErr.Code)
			require.NotEqual(t, "protocol_error", rpcErr.Code)
		})
	}
}

func TestRPCRejectsUnsupportedMethodAndMismatchedResponse(t *testing.T) {
	for _, tc := range []struct{ id, code, want string }{
		{"0", "protocol_error", "daemon_incompatible"},
		{"other", "protocol_error", "invalid BrowserSkill response ID"},
		{"0", "permission_denied", "invalid BrowserSkill response ID"},
	} {
		t.Run(tc.id+tc.code, func(t *testing.T) {
			home, err := os.MkdirTemp("", "bsk-wire-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(home) })
			require.NoError(t, os.Mkdir(filepath.Join(home, "run"), 0o700))
			listener, err := net.Listen("unix", filepath.Join(home, "run", "daemon.sock"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer func() { _ = conn.Close() }()
				var request map[string]any
				if err = json.NewDecoder(conn).Decode(&request); err == nil {
					err = json.NewEncoder(conn).Encode(rpcReply{
						ID: tc.id, Error: &RPCError{Code: tc.code, Message: "unsupported request"},
					})
				}
				done <- err
			}()
			_, err = rpc(t.Context(), &device{runtime: &daemon{home: home}},
				"tool.wheel", map[string]any{"delta_y": 500})
			require.ErrorContains(t, err, tc.want)
			require.NoError(t, <-done)
		})
	}
}
