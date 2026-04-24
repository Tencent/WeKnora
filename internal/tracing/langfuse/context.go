package langfuse

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// traceCtxKey is the exported context key defined in types/const.go. It lives
// there (not inside this package) so that logger.CloneContext — which rebuilds
// a stripped-down context on every request — can preserve the Langfuse trace
// without importing this package. If we kept the key private here, every
// CloneContext call would drop the trace and downstream LLM wrappers would
// each auto-create their own shallow trace, fragmenting a single HTTP request
// into many unrelated traces in the Langfuse UI.
var traceCtxKey = types.LangfuseTraceContextKey

// parentObsCtxKey tracks the current "parent observation" id — i.e. the span
// that encloses any generation/span started under it. Unlike traceCtxKey, this
// key is private: spans are a pure tracer concern and logger.CloneContext
// should reset the parent across request boundaries (we don't want a leftover
// span id from one request leaking into another's LLM calls).
type parentObsCtxKeyType struct{}

var parentObsCtxKey = parentObsCtxKeyType{}

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

// withParentObservation stores the id of the enclosing span so children
// (sub-spans or generations) can attach via parentObservationId.
func withParentObservation(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, parentObsCtxKey, id)
}

// parentObservationFromCtx returns the enclosing span id if any.
func parentObservationFromCtx(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(parentObsCtxKey).(string)
	return v, ok && v != ""
}
