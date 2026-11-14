package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

const sseTestBudget = 150 * time.Millisecond

func newBoundedSSETestConnection(t *testing.T, handler http.HandlerFunc) *boundedSSEConnection {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	sse, err := transport.NewSSE(server.URL, transport.WithHTTPClient(server.Client()))
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
	for _, mode := range []string{"post_headers", "post_body", "event_response", "notification", "cancelled"} {
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
		if mode == "event_response" {
			w.WriteHeader(http.StatusAccepted)
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
	}
	start := time.Now()
	err := sendSSETestOperation(ctx, c, mode)
	require.Error(t, err)
	require.Less(t, time.Since(start), 750*time.Millisecond)
	if mode != "cancelled" {
		require.NoError(t, ctx.Err(), "service budget must expire before the caller budget")
	} else {
		require.ErrorIs(t, err, context.Canceled)
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
