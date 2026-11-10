package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"
)

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
	return c.SSE.SendRequest(ctx, request)
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
