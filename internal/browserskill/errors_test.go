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
