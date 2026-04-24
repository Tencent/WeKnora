package langfuse

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// InjectTracing stamps the current trace/span ids (and a best-effort
// user/session label) from ctx onto the given payload, provided the payload
// embeds types.TracingContext (and thus implements LangfuseTracingCarrier).
//
// Safe to call unconditionally: when Langfuse is disabled or no trace is
// present on ctx, it writes a zero-valued TracingContext — which round-trips
// through JSON as absent fields and therefore costs nothing.
//
// Call sites live at every asynq.NewTask creation point. We deliberately
// keep the API synchronous and mutating (rather than returning a new payload
// copy) so that existing enqueue code needs only a single added line just
// before json.Marshal, minimizing review surface and the risk of regressions.
func InjectTracing(ctx context.Context, carrier types.LangfuseTracingCarrier) {
	if carrier == nil {
		return
	}
	mgr := GetManager()
	if !mgr.Enabled() {
		return
	}
	tc := types.TracingContext{}
	if trace, ok := TraceFromContext(ctx); ok && trace != nil {
		tc.LangfuseTraceID = trace.ID
	}
	if obs, ok := parentObservationFromCtx(ctx); ok {
		tc.LangfuseParentObservationID = obs
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
//  1. Parses any tracing ids embedded in the task payload and either
//     resumes the originating trace (so the Langfuse UI stitches the HTTP
//     request and the async processing into one tree), or — for tasks that
//     came from a scheduled job with no originating HTTP request — creates
//     a standalone trace tagged with the task type.
//
//  2. Opens a SPAN around the handler execution so every child generation
//     (embedding / VLM / chat / rerank / ASR) auto-attaches to it via
//     parentObservationId.
//
//  3. Enriches the span with asynq's own metadata: task id, queue, retry
//     count, payload size.
//
// When the manager is disabled it degrades to a pass-through; failure of
// the Langfuse path never blocks task execution.
//
// Register it once in router/task.go via mux.Use.
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

			// If the upstream enqueuer stamped a trace id onto the payload,
			// we graft under that trace. Otherwise (scheduled jobs, legacy
			// in-flight tasks that predate this code, tests, etc.) we start
			// a standalone trace named after the task type so at least the
			// worker-side work is observable.
			var trace *Trace
			shouldFinishTrace := false
			if tc.LangfuseTraceID != "" {
				ctx, trace = mgr.ResumeTrace(ctx, tc.LangfuseTraceID, tc.LangfuseParentObservationID)
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
// the full JSON blob because:
//
//   - Manual/text-ingest payloads can be many kilobytes of prose;
//   - FAQ import payloads embed the full entry list;
//   - Document-process payloads contain file URLs that, while small, may
//     include presigned query strings that rotate (adding diff noise).
//
// Instead we preview the first ~1KB verbatim, which matches what Langfuse
// itself would display and keeps ingestion bandwidth predictable.
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
// (no gin.Context) so both HTTP and asynq paths can share the same fallback
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
