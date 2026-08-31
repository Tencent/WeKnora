package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestLegacySSETransportSendsPostAndReadsEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n")
	}))
	defer server.Close()

	legacy, err := newLegacySSETransport(server.URL, server.Client(), nil, transport.OAuthConfig{}, false)
	require.NoError(t, err)
	require.NoError(t, legacy.Start(context.Background()))

	response, err := legacy.SendRequest(context.Background(), transport.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcpgo.NewRequestId(int64(1)),
		Method:  "initialize",
	})
	require.NoError(t, err)
	require.Equal(t, "1", response.ID.String())
	require.JSONEq(t, `{"ok":true}`, string(response.Result))
}
