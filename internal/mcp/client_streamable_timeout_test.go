package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// Model a deadline timer whose cancellation callback has not been scheduled
// yet. The HTTP client's independent timer can interrupt its body read first.
type delayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c delayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestStreamableHTTPListToolsResponseBody(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	for _, tc := range []struct {
		mode    string
		wantErr error
		message string
	}{
		{mode: "service deadline", wantErr: context.DeadlineExceeded},
		{mode: "caller deadline", wantErr: context.DeadlineExceeded},
		{mode: "delayed deadline signal", wantErr: context.DeadlineExceeded},
		{mode: "caller cancellation", wantErr: context.Canceled},
		{mode: "early EOF"},
		{mode: "HTTP error with stalled body", message: "status 503"},
		{mode: "RPC error", message: "directory disabled"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			handler := server.NewStreamableHTTPServer(
				server.NewMCPServer("peer", "1", server.WithToolCapabilities(false)), server.WithStateLess(true),
			)
			wait := 5 * time.Second
			if tc.mode == "caller deadline" || tc.mode == "HTTP error with stalled body" {
				wait = 250 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(sdk.HeaderMethod) != string(sdk.MethodToolsList) {
					handler.ServeHTTP(w, r)
					return
				}
				var request struct{ ID json.RawMessage }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if tc.mode == "HTTP error with stalled body" {
					w.WriteHeader(http.StatusServiceUnavailable)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, ": heartbeat\n\n")
				w.(http.Flusher).Flush()
				switch tc.mode {
				case "early EOF":
					return
				case "RPC error":
					const rpcError = `{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"directory disabled"}}`
					_, _ = fmt.Fprintf(w, "data: "+rpcError+"\n\n", request.ID)
					return
				case "caller cancellation":
					cancel()
				}
				<-r.Context().Done()
			}))
			t.Cleanup(upstream.Close)
			manager := NewMCPManager(nil)
			t.Cleanup(manager.Shutdown)
			client, err := manager.GetOrCreateClient(t.Context(), &types.MCPService{
				ID: "peer", Enabled: true, URL: &upstream.URL, TransportType: types.MCPTransportHTTPStreamable,
				AdvancedConfig: &types.MCPAdvancedConfig{Timeout: 1},
			})
			require.NoError(t, err)
			ctx, stop := context.WithTimeout(ctx, wait)
			defer stop()
			if tc.mode == "delayed deadline signal" {
				ctx = delayedDeadlineContext{Context: ctx, deadline: time.Now().Add(250 * time.Millisecond)}
			}
			start := time.Now()
			_, err = client.ListTools(ctx)
			require.Error(t, err)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NotErrorIs(t, err, context.DeadlineExceeded)
				require.NotErrorIs(t, err, context.Canceled)
			}
			if tc.message != "" {
				require.ErrorContains(t, err, tc.message)
			}
			if tc.mode == "service deadline" {
				require.GreaterOrEqual(t, time.Since(start), time.Second)
				require.NoError(t, ctx.Err(), "the service timeout must expire before the caller's deadline")
			}
		})
	}
}
