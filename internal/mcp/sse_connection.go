package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"
)

// mcp-go v1.1.1 reads the entire SSE POST acknowledgement before checking
// its HTTP status. A failed read must not hide a rejection already received.
type ssePostTransport struct {
	base http.RoundTripper
}

func (t ssePostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || req.Method != http.MethodPost || resp.StatusCode < http.StatusBadRequest {
		return resp, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// The SDK uses WWW-Authenticate and does not need the challenge body.
		_ = resp.Body.Close()
		resp.Body = http.NoBody
		resp.ContentLength = 0
	} else {
		resp.Body = sseErrorBody{ReadCloser: resp.Body, ctx: req.Context()}
	}
	return resp, nil
}

// The SDK includes this body in its HTTP error. Preserve any diagnostic text
// received (including session errors), even if the rest never arrives.
// An explicit caller cancellation still propagates its original read error.
type sseErrorBody struct {
	io.ReadCloser
	ctx context.Context
}

func (b sseErrorBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.ctx.Err() != context.Canceled {
		return n, io.EOF
	}
	return n, err
}

// boundedSSEConnection separates establishment and RPC budgets from the
// persistent event stream. Embedding SSE preserves its optional transport hooks.
type boundedSSEConnection struct {
	*transport.SSE
	timeout time.Duration
	mu      sync.Mutex
	cancel  context.CancelCauseFunc
}

func (c *boundedSSEConnection) Start(ctx context.Context) error {
	lifetime, cancel := context.WithCancelCause(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	// Unlike WithTimeout, this timer can be stopped without cancelling the
	// successful stream. It covers headers as well as the endpoint event.
	timer := time.AfterFunc(c.timeout, func() { cancel(context.DeadlineExceeded) })
	err := c.SSE.Start(lifetime)
	if !timer.Stop() {
		cancel(context.DeadlineExceeded)
	}
	if cause := context.Cause(lifetime); cause != nil {
		err = cause
	}
	if err != nil {
		cancel(err)
	}
	return err
}

func (c *boundedSSEConnection) SendRequest(
	ctx context.Context, request transport.JSONRPCRequest,
) (*transport.JSONRPCResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	// Includes both POST acknowledgement and waiting for its SSE response.
	response, err := c.SSE.SendRequest(ctx, request)
	// After draining the POST body, mcp-go v1.1.1 can see an elapsed
	// deadline before the context timer runs and return (nil, ctx.Err()).
	if response == nil && err == nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return nil, context.DeadlineExceeded
		}
	}
	// mcp-go v1.1.1 races its own response timer against ctx.Done(). Only
	// that timer returns this untyped error. HTTP and RPC errors stay intact.
	if err != nil && strings.HasPrefix(err.Error(), "timeout waiting for SSE response after ") {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, context.DeadlineExceeded
	}
	return response, err
}

func (c *boundedSSEConnection) SendNotification(ctx context.Context, notification sdk.JSONRPCNotification) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.SSE.SendNotification(ctx, notification)
}

func (c *boundedSSEConnection) Close() error {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel(context.Canceled)
	}
	return c.SSE.Close()
}
