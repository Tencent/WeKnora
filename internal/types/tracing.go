package types

// TracingContext is an embeddable struct that carries observability context
// (currently Langfuse trace/span ids plus user/session hints) across process
// boundaries — specifically, from an HTTP request into an asynq task payload
// and back out inside the worker.
//
// It lives in the types package, not the langfuse package, so that:
//
//   - asynq payload structs can embed it without pulling the langfuse
//     package into every service/handler import graph;
//   - the langfuse package can remain a leaf dependency that only types
//     (and its own tests) reference directly.
//
// The JSON tags all use the "lf_" prefix and omitempty so that payloads
// constructed before the Langfuse feature landed remain byte-compatible
// (empty fields collapse to nothing in the serialized output) and so that
// Langfuse-specific columns don't collide with business fields that may
// happen to be named similarly.
type TracingContext struct {
	// LangfuseTraceID is the id of the root trace that originated this task.
	// When set, workers attach their observations to the same trace instead
	// of creating a standalone one, which is what makes the Langfuse UI
	// show the asynq work as a child of the originating HTTP request.
	LangfuseTraceID string `json:"lf_trace_id,omitempty"`
	// LangfuseParentObservationID, when set, points to the span that
	// enclosed the enqueue call. The worker's own span/generation will list
	// it as parentObservationId so Langfuse renders the tree as:
	// http-trace → http-span → asynq-span → generation.
	LangfuseParentObservationID string `json:"lf_parent_obs_id,omitempty"`
	// LangfuseUserID preserves the userId / tenant label across the async
	// boundary so that orphan async traces (when no upstream trace id is
	// available) still show up in the Langfuse "Users" view under the
	// right tenant.
	LangfuseUserID string `json:"lf_user_id,omitempty"`
	// LangfuseSessionID preserves the sessionId for the same reason.
	LangfuseSessionID string `json:"lf_session_id,omitempty"`
}

// SetLangfuseTracing overwrites the embedded TracingContext. Method is
// exported so helpers in internal/tracing/langfuse can populate it via the
// LangfuseTracingCarrier interface without reflection.
func (tc *TracingContext) SetLangfuseTracing(other TracingContext) {
	*tc = other
}

// GetLangfuseTracing returns a copy of the embedded TracingContext.
func (tc TracingContext) GetLangfuseTracing() TracingContext {
	return tc
}

// LangfuseTracingCarrier is implemented automatically by any struct that
// embeds TracingContext (thanks to Go's method promotion rules). The asynq
// enqueue helper in internal/tracing/langfuse uses this interface to inject
// the current trace/span ids into the payload without caring about the
// concrete payload type.
type LangfuseTracingCarrier interface {
	SetLangfuseTracing(TracingContext)
	GetLangfuseTracing() TracingContext
}
