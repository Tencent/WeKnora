package langfuse

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// InjectTracing stamps the current W3C traceparent (plus a best-effort
// user/session label) from ctx onto the given payload, provided the payload
// embeds types.TracingContext (and thus implements LangfuseTracingCarrier).
//
// The traceparent is produced by propagator.Inject from the active OTel span
// context (the HTTP root span opened by GinMiddleware). The asynq worker
// re-extracts it so worker-side spans are children of the same trace —
// giving LiteFuse one stitched tree across the HTTP request and the async
// processing. This also makes a sop3 run's traceparent propagate through to
// any asynq jobs WeKnora enqueues while serving sop3's agent-chat call.
//
// Safe to call unconditionally: when Langfuse is disabled or no span is
// present on ctx, it writes a zero-valued TracingContext — which round-trips
// through JSON as absent fields and costs nothing.
func InjectTracing(ctx context.Context, carrier types.LangfuseTracingCarrier) {
	if carrier == nil {
		return
	}
	mgr := GetManager()
	if !mgr.Enabled() {
		return
	}
	tc := types.TracingContext{}
	c := propagation.MapCarrier{}
	propagator.Inject(ctx, c)
	tc.LangfuseTraceparent = c["traceparent"]
	// Backward-compat: keep LangfuseTraceID = the W3C trace id for any legacy
	// reader. LangfuseParentObservationID is no longer used by the OTLP path.
	if trace, ok := TraceFromContext(ctx); ok && trace != nil {
		tc.LangfuseTraceID = trace.ID
	}
	tc.LangfuseUserID = userIDFromCtx(ctx)
	tc.LangfuseSessionID = sessionIDFromCtx(ctx)
	carrier.SetLangfuseTracing(tc)
}

// peekTracingContext pulls just the Langfuse tracing fields out of a raw
// asynq payload. It's deliberately lax: every payload type in the project
// is a JSON object at the top level, and absent/mismatched fields decode to
// the zero value. If unmarshalling fails entirely (e.g. the payload isn't
// JSON at all) we return a zero TracingContext and let the main handler
// deal with its own error — we never want an observability bug to mask a
// real task failure.
func peekTracingContext(payload []byte) types.TracingContext {
	if len(payload) == 0 {
		return types.TracingContext{}
	}
	var tc types.TracingContext
	_ = json.Unmarshal(payload, &tc)
	return tc
}

// AsynqMiddleware is the worker-side counterpart of GinMiddleware. It:
//
//  1. Extracts the W3C traceparent stamped onto the task payload by
//     InjectTracing and resumes the originating trace (so the Langfuse UI
//     stitches the HTTP request and the async processing into one tree). For
//     scheduled jobs with no upstream trace it opens a standalone trace named
//     after the task type.
//
//  2. Opens a SPAN around the handler execution so every child generation
//     (embedding / VLM / chat / rerank / ASR) auto-attaches to it.
//
//  3. Enriches the span with asynq's own metadata: task id, queue, retry
//     count, payload size.
//
// When the manager is disabled it degrades to a pass-through; failure of the
// Langfuse path never blocks task execution. Register once via mux.Use.
func AsynqMiddleware() asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			mgr := GetManager()
			if !mgr.Enabled() {
				return next.ProcessTask(ctx, task)
			}

			tc := peekTracingContext(task.Payload())
			taskID, _ := asynq.GetTaskID(ctx)
			retryCount, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			queueName, _ := asynq.GetQueueName(ctx)

			meta := map[string]interface{}{
				"task_type":     task.Type(),
				"task_id":       taskID,
				"queue":         queueName,
				"retry":         retryCount,
				"max_retry":     maxRetry,
				"payload_bytes": len(task.Payload()),
			}

			// If the upstream enqueuer stamped a traceparent, resume that trace
			// (worker spans become children of the HTTP trace). Otherwise start
			// a standalone trace named after the task type.
			var trace *Trace
			shouldFinishTrace := false
			if tc.LangfuseTraceparent != "" {
				ctx = propagator.Extract(ctx, propagation.MapCarrier{"traceparent": tc.LangfuseTraceparent})
				if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
					ctx = withTrace(ctx, &Trace{ID: sc.TraceID().String(), manager: mgr})
				}
			} else {
				ctx, trace = mgr.StartTrace(ctx, TraceOptions{
					Name:      "asynq." + task.Type(),
					UserID:    firstNonEmptyString(tc.LangfuseUserID, userIDFromCtx(ctx)),
					SessionID: firstNonEmptyString(tc.LangfuseSessionID, sessionIDFromCtx(ctx)),
					Metadata:  meta,
					Tags:      []string{"asynq", task.Type()},
				})
				shouldFinishTrace = true
			}

			ctx, span := mgr.StartSpan(ctx, SpanOptions{
				Name:     "asynq." + task.Type(),
				Input:    spanInputFromPayload(task.Payload()),
				Metadata: meta,
			})

			err := next.ProcessTask(ctx, task)

			outcome := "success"
			if err != nil {
				outcome = "error"
			}
			span.Finish(map[string]interface{}{
				"outcome": outcome,
			}, map[string]interface{}{
				"outcome": outcome,
			}, err)

			if shouldFinishTrace {
				trace.Finish(map[string]interface{}{
					"outcome": outcome,
				}, map[string]interface{}{
					"task_type": task.Type(),
					"outcome":   outcome,
				})
			}

			return err
		})
	}
}

// spanInputFromPayload surfaces a compact, human-readable summary of the
// task payload for the Langfuse "Input" pane. We deliberately do NOT send
// the full JSON blob because manual/text-ingest payloads can be many
// kilobytes and FAQ import payloads embed the full entry list. Instead we
// preview the first ~1KB verbatim.
func spanInputFromPayload(payload []byte) interface{} {
	const preview = 1024
	if len(payload) == 0 {
		return nil
	}
	if len(payload) <= preview {
		return string(payload)
	}
	return map[string]interface{}{
		"preview": string(payload[:preview]) + "...",
		"bytes":   len(payload),
	}
}

// userIDFromCtx mirrors middleware.extractUserID but accepts a raw context
// (no gin.Context) so both HTTP and asynq paths share the same fallback
// logic: explicit UserID → tenant:<id> → empty.
func userIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(types.UserIDContextKey).(string); ok && v != "" {
		return v
	}
	if v, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok && v != 0 {
		return "tenant:" + strconv.FormatUint(v, 10)
	}
	return ""
}

// sessionIDFromCtx pulls a best-effort "session" label. For HTTP chat this
// is already set by GinMiddleware; for async work we fall back to the
// request id so retries of the same logical task group together.
func sessionIDFromCtx(ctx context.Context) string {
	if v, ok := types.RequestIDFromContext(ctx); ok && v != "" {
		return v
	}
	return ""
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
