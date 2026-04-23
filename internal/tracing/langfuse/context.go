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
