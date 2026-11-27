package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

const sseTestBudget = 150 * time.Millisecond

func newBoundedSSETestConnection(t *testing.T, handler http.HandlerFunc) *boundedSSEConnection {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	sse, err := transport.NewSSE(
		server.URL, transport.WithHTTPClient(server.Client()),
		// Exercise the SDK's response timer before the service context expires.
		transport.WithResponseTimeout(sseTestBudget/2),
	)
	require.NoError(t, err)
	c := &boundedSSEConnection{SSE: sse, timeout: sseTestBudget}
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c
}

func TestSSEStartupBudgetAndCancellation(t *testing.T) {
	for _, mode := range []string{"headers", "endpoint", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			started, closed := make(chan struct{}), make(chan struct{})
			c := newBoundedSSETestConnection(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				defer close(closed)
				if mode == "endpoint" {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Start(ctx) }()
			<-started
			want := context.DeadlineExceeded
			if mode == "cancelled" {
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, want)
			case <-time.After(time.Second):
				t.Fatal("SSE establishment did not stop")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed establishment left the HTTP request running")
			}
		})
	}
}

func TestSSERPCBudgetKeepsEventStreamAlive(t *testing.T) {
	for _, mode := range []string{
		"post_headers", "post_body", "event_response", "ack_at_deadline", "notification", "cancelled", "server_error",
	} {
		t.Run(mode, func(t *testing.T) { testSSERPCBudget(t, mode) })
	}
}

func testSSERPCBudget(t *testing.T, mode string) {
	t.Helper()
	streamClosed := make(chan struct{})
	c := newBoundedSSETestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /message\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(streamClosed)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if mode == "event_response" || mode == "ack_at_deadline" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if mode == "server_error" {
			http.Error(w, "timeout waiting for SSE response after 1s", http.StatusServiceUnavailable)
			return
		}
		if mode == "post_body" {
			w.WriteHeader(http.StatusAccepted)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	})
	require.NoError(t, c.Start(t.Context()))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if mode == "cancelled" {
		cancel()
	} else if mode == "ack_at_deadline" {
		ctx = delayedDeadlineContext{Context: ctx, deadline: time.Now()}
	}
	start := time.Now()
	err := sendSSETestOperation(ctx, c, mode)
	require.Error(t, err)
	require.Less(t, time.Since(start), 750*time.Millisecond)
	switch mode {
	case "cancelled":
		require.ErrorIs(t, err, context.Canceled)
	case "server_error":
		require.ErrorContains(t, err, "status 503")
		require.NotErrorIs(t, err, context.DeadlineExceeded)
	default:
		require.NoError(t, ctx.Err(), "service budget must expire before the caller budget")
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	select {
	case <-streamClosed:
		t.Fatal("an RPC timeout or cancellation must not close the event stream")
	default:
	}
	require.NoError(t, c.Close())
	select {
	case <-streamClosed:
	case <-time.After(time.Second):
		t.Fatal("explicit Close did not release the event stream")
	}
}

func sendSSETestOperation(ctx context.Context, c *boundedSSEConnection, mode string) error {
	if mode == "notification" {
		return c.SendNotification(ctx, sdk.JSONRPCNotification{
			JSONRPC: "2.0", Notification: sdk.Notification{Method: "notifications/initialized"},
		})
	}
	_, err := c.SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: "2.0", ID: sdk.NewRequestId(1), Method: "tools/list",
	})
	return err
}

func TestSSEListToolsWithStalledHTTPErrorBody(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	peer := server.NewSSEServer(server.NewMCPServer("peer", "1", server.WithToolCapabilities(false)))
	var rejection atomic.Int32
	const metadataPath = "/.well-known/oauth-protected-resource"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := int(rejection.Load())
		if r.Method != http.MethodPost || status == 0 {
			peer.ServeHTTP(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if status == http.StatusUnauthorized {
			challenge := fmt.Sprintf(`Bearer resource_metadata="http://%s%s"`, r.Host, metadataPath)
			w.Header().Set("WWW-Authenticate", challenge)
		}
		if status == http.StatusBadRequest {
			http.Error(w, "Invalid session ID", status)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusServiceUnavailable {
			_, _ = io.WriteString(w, "under maintenance")
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	url := upstream.URL + "/sse"
	client, err := manager.GetOrCreateClient(t.Context(), &types.MCPService{
		ID: "peer", Enabled: true, URL: &url, TransportType: types.MCPTransportSSE,
	})
	require.NoError(t, err)

	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rejection.Store(int32(status))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := client.ListTools(ctx)
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			if status == http.StatusUnauthorized {
				require.NoError(t, ctx.Err(), "a challenge should be returned without waiting for its body")
				var auth *transport.AuthorizationRequiredError
				require.ErrorAs(t, err, &auth)
				require.Equal(t, upstream.URL+metadataPath, auth.ResourceMetadataURL)
			} else {
				require.ErrorContains(t, err, "status 503: under maintenance")
			}
		})
	}

	// A rejected POST must leave the event stream usable for the next request.
	rejection.Store(0)
	_, err = client.ListTools(t.Context())
	require.NoError(t, err)

	// Ordinary error text still drives the manager's session invalidation.
	rejection.Store(http.StatusBadRequest)
	_, err = client.ListTools(t.Context())
	require.ErrorContains(t, err, "Invalid session ID")
	require.False(t, client.IsConnected())
}

func TestSSERejectedPostPreservesCallerCancellation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	for _, cause := range []error{context.Canceled, errors.New("chat stopped")} {
		t.Run(cause.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL, nil)
			require.NoError(t, err)
			response, err := (ssePostTransport{base: upstream.Client().Transport}).RoundTrip(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = response.Body.Close() })

			// Wait for actual response data, so cancellation happens during the body read.
			_, err = io.ReadFull(response.Body, make([]byte, len("partial")))
			require.NoError(t, err)
			cancel(cause)
			_, err = io.ReadAll(response.Body)
			require.ErrorIs(t, err, cause)
		})
	}
}
