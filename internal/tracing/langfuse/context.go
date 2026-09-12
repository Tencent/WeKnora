package langfuse

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// traceCtxKey is the exported context key defined in types/const.go. It lives
// there (not inside this package) so that logger.CloneContext — which rebuilds
// a stripped-down context on every request — can preserve the Langfuse trace
// without importing this package. If we kept the key private here, every
// CloneContext call would drop the trace and downstream LLM wrappers would
// each auto-create their own shallow trace, fragmenting a single HTTP request
// into many unrelated traces in the Langfuse UI.
//
// Span parenting is NOT carried by this key: it flows through the standard
// OpenTelemetry context (trace.SpanFromContext), which tracer.Start wires up
// automatically. The *Trace on this key is only for handlers/middleware that
// need to set trace-level input/output.
var traceCtxKey = types.LangfuseTraceContextKey

// withTrace stores a *Trace on the context so downstream LLM wrappers can
// attach their generations to it.
func withTrace(ctx context.Context, t *Trace) context.Context {
	if t == nil || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, traceCtxKey, t)
}

// traceFromCtx retrieves the active trace, if any.
func traceFromCtx(ctx context.Context) (*Trace, bool) {
	if ctx == nil {
		return nil, false
	}
	t, ok := ctx.Value(traceCtxKey).(*Trace)
	return t, ok && t != nil
}

// TraceFromContext is the public accessor used by HTTP middlewares and
// handlers that want to set the trace input/output on the active trace.
func TraceFromContext(ctx context.Context) (*Trace, bool) {
	return traceFromCtx(ctx)
}

// TraceparentFromContext returns the W3C traceparent for the active span, or
// empty when Langfuse is disabled or ctx carries no span. Used to stamp the
// originating chat trace onto derived work (follow-up suggestions) that may
// run after the HTTP handler returns, or on a later request.
func TraceparentFromContext(ctx context.Context) string {
	mgr := GetManager()
	if ctx == nil || !mgr.Enabled() {
		return ""
	}
	c := propagation.MapCarrier{}
	propagator.Inject(ctx, c)
	return c["traceparent"]
}

// AttachTraceparent resumes the originating trace from a W3C traceparent
// previously captured by TraceparentFromContext / InjectTracing. When ctx
// already has a *Trace (same-request background work), it is left unchanged
// so we do not replace a live local parent with a remote one.
//
// This is the in-process counterpart of AsynqMiddleware's extract path: a
// later HTTP request (e.g. POST .../suggestions) has no GinMiddleware trace,
// and without this StartGeneration would auto-create an orphan root named
// after the LLM call.
func AttachTraceparent(ctx context.Context, traceparent string) context.Context {
	mgr := GetManager()
	if ctx == nil || traceparent == "" || !mgr.Enabled() {
		return ctx
	}
	if _, ok := traceFromCtx(ctx); ok {
		return ctx
	}
	ctx = propagator.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		ctx = withTrace(ctx, &Trace{ID: sc.TraceID().String(), manager: mgr})
	}
	return ctx
}
