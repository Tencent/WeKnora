package operations

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const maxResponseBytes = 2 << 20

// Identity is who an operation runs as. Only a signed-in web user can be
// dispatched: API-key, embed and IM principals carry narrower grants than the
// user record behind them, and replaying a call as that user would widen them.
type Identity struct {
	UserID   string
	TenantID uint64
	Language string
	Agent    types.AuditAgentAction
}

// Response is a captured HTTP response.
type Response struct {
	Status int
	Body   []byte
}

// Dispatcher executes a built call as identity.
type Dispatcher interface {
	Dispatch(ctx context.Context, identity Identity, call Call) (*Response, error)
}

// ErrDispatcherUnavailable means the router has not been bound yet.
var ErrDispatcherUnavailable = errors.New("operation dispatcher is not available")

// ErrResponseTooLarge means the handler wrote more than the capture limit.
var ErrResponseTooLarge = errors.New("operation response is too large")

// HandlerDispatcher sends calls through an http.Handler, normally the gin
// engine. The engine is built after the services that hold this dispatcher,
// so it is bound late.
type HandlerDispatcher struct {
	mu      sync.RWMutex
	handler http.Handler
}

// NewHandlerDispatcher returns a dispatcher that fails until Bind is called.
func NewHandlerDispatcher() *HandlerDispatcher { return &HandlerDispatcher{} }

// Bind sets the handler calls are dispatched to.
func (d *HandlerDispatcher) Bind(h http.Handler) {
	d.mu.Lock()
	d.handler = h
	d.mu.Unlock()
}

// Dispatch serves call through the bound handler as identity, detached from
// ctx's values but cancelled with it.
func (d *HandlerDispatcher) Dispatch(ctx context.Context, identity Identity, call Call) (*Response, error) {
	d.mu.RLock()
	h := d.handler
	d.mu.RUnlock()
	if h == nil {
		return nil, ErrDispatcherUnavailable
	}

	// The request must not inherit the agent run's context values (KB grants,
	// tenant overrides, caller): handlers would treat them as already
	// authorized. Only cancellation crosses over; Auth rebuilds the rest.
	reqCtx := types.WithInternalDispatch(detachValues(ctx), types.InternalDispatch{
		UserID: identity.UserID, TenantID: identity.TenantID,
	})
	if _, ok := types.InternalDispatchFromContext(reqCtx); !ok {
		return nil, errors.New("operation identity is incomplete")
	}
	reqCtx = types.WithAuditAgentAction(reqCtx, identity.Agent)

	req, err := http.NewRequestWithContext(reqCtx, call.Method, call.URI(), bytes.NewReader(call.Body))
	if err != nil {
		return nil, err
	}
	// TEST-NET-1 peer address: never loopback or a private network, so no
	// local-trust shortcut can apply to a dispatched request.
	req.RemoteAddr = "192.0.2.1:0"
	req.Header.Set("Accept", "application/json")
	if call.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if identity.Language != "" {
		req.Header.Set("Accept-Language", identity.Language)
	}

	w := newCaptureWriter()
	h.ServeHTTP(w, req)
	if w.overflow {
		return nil, ErrResponseTooLarge
	}
	return &Response{Status: w.status(), Body: w.body.Bytes()}, nil
}

// detachValues keeps ctx's deadline and cancellation but none of its values.
func detachValues(ctx context.Context) context.Context { return valuelessContext{ctx} }

type valuelessContext struct{ parent context.Context }

func (c valuelessContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c valuelessContext) Done() <-chan struct{}       { return c.parent.Done() }
func (c valuelessContext) Err() error                  { return c.parent.Err() }
func (c valuelessContext) Value(any) any               { return nil }

type captureWriter struct {
	header   http.Header
	code     int
	body     bytes.Buffer
	overflow bool
}

func newCaptureWriter() *captureWriter { return &captureWriter{header: http.Header{}} }

func (w *captureWriter) Header() http.Header { return w.header }

func (w *captureWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	if w.body.Len()+len(p) > maxResponseBytes {
		w.overflow = true
		return 0, ErrResponseTooLarge
	}
	return w.body.Write(p)
}

// Flush satisfies http.Flusher for handlers that flush; nothing is streamed.
func (w *captureWriter) Flush() {}

func (w *captureWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}
