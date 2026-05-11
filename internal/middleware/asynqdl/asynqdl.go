// Package asynqdl provides an asynq middleware that records every task
// whose retry budget is exhausted into the generic task_dead_letters table.
//
// The middleware is the catch-all observability path for asynq failures:
// without it, archived tasks live only inside Redis with the asynq
// inspector CLI as the sole readable surface. With it, operators can
// SQL-query failures by task type, scope, or tenant.
//
// The package is intentionally separate from /internal/middleware (which
// is gin-only) and from /internal/tracing/langfuse (which has its own
// mux.Use). Putting it in its own package keeps the import graph tidy:
// it depends on types/interfaces and asynq, nothing else inside the
// project.
package asynqdl

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// Middleware returns an asynq.MiddlewareFunc that records dead-lettered
// tasks. It is safe to install before any other asynq middleware: the
// inner handler's return value is forwarded verbatim, so every layer
// (langfuse tracing, business handlers) still sees the original
// error/result.
//
// Behaviour:
//
//   - Tasks that succeed (handler returns nil) are passed through with
//     zero overhead besides the function call.
//
//   - Tasks that fail (handler returns non-nil) trigger an inspection of
//     asynq's retry counters. We only record on the FINAL retry —
//     i.e. when GetRetryCount(ctx) >= GetMaxRetry(ctx). Earlier failures
//     are still reported up to asynq for backoff/retry but do not
//     create dead-letter rows; otherwise we'd write one row per
//     transient hiccup.
//
//   - Insert is best-effort: a DB error is logged and swallowed. The
//     middleware never alters the task error returned to the caller.
//
// This middleware should be installed once on the asynq mux, BEFORE
// other middlewares that may transform errors. See router/task.go.
func Middleware(repo interfaces.TaskDeadLetterRepository) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			err := next.ProcessTask(ctx, t)
			if err == nil {
				return nil
			}
			if !isFinalAttempt(ctx) {
				return err
			}
			if repo == nil {
				return err
			}
			// Capture the actual attempt count from asynq so the
			// dead-letter row reflects how many tries were made
			// before the task was archived. On the final attempt
			// asynq's counter satisfies retried == maxRetry, so the
			// total attempt count is retried + 1 (initial try +
			// retries). Both helpers can fail outside a worker ctx;
			// in that case we record 0 so it's clear the count
			// wasn't observable.
			attempts := 0
			if retried, ok := asynq.GetRetryCount(ctx); ok {
				attempts = retried + 1
			}
			dl := buildDeadLetter(t, err, attempts)
			if dl == nil {
				return err
			}
			// context.Background() so a cancelled task ctx (e.g. server
			// shutdown signaling cancellation right as a task fails)
			// doesn't also drop the dead-letter record.
			if insertErr := repo.Insert(context.Background(), dl); insertErr != nil {
				logger.Warnf(ctx,
					"asynq dead-letter: failed to record %s task: %v (original task error: %v)",
					t.Type(), insertErr, err,
				)
			}
			return err
		})
	}
}

// isFinalAttempt reports whether the just-finished handler invocation
// was the LAST one asynq will run before archiving the task. Asynq
// increments retry_count *after* a successful retry, so on the final
// attempt retry_count == max_retry. Both helpers can fail (returning
// 0, false) when the middleware runs outside an asynq worker context;
// in that case we conservatively report "final" so test setups that
// invoke the middleware directly still exercise the insert path.
func isFinalAttempt(ctx context.Context) bool {
	retried, retriedOK := asynq.GetRetryCount(ctx)
	maxRetry, maxOK := asynq.GetMaxRetry(ctx)
	if !retriedOK || !maxOK {
		return true
	}
	return retried >= maxRetry
}

// buildDeadLetter constructs a TaskDeadLetter from the task and the
// final error. The payload extraction is deliberately schema-agnostic:
// every project payload is a JSON object containing some subset of the
// well-known fields {tenant_id, knowledge_base_id, kb_id, knowledge_id,
// task_id}. We probe for those fields with a tolerant struct so a new
// payload shape doesn't need to know about this middleware.
//
// Returns nil only if the task itself is nil — every other failure mode
// (unparseable payload, unknown task type) still produces a row, with
// scope="unknown" so operators can find it.
func buildDeadLetter(t *asynq.Task, taskErr error, attempts int) *types.TaskDeadLetter {
	if t == nil {
		return nil
	}
	probe := payloadProbe{}
	_ = json.Unmarshal(t.Payload(), &probe) // best-effort

	scope, scopeID := inferScope(probe)
	relatedID := probe.KnowledgeID

	// asynq stores the payload as raw bytes; preserve it verbatim so a
	// future requeue can reuse it directly.
	rawPayload := t.Payload()
	if len(rawPayload) == 0 {
		rawPayload = []byte("{}")
	}

	return &types.TaskDeadLetter{
		TenantID:  probe.TenantID,
		TaskType:  t.Type(),
		Scope:     scope,
		ScopeID:   scopeID,
		RelatedID: relatedID,
		Payload:   json.RawMessage(rawPayload),
		LastError: truncateError(taskErr.Error(), 8192),
		FailCount: attempts,
	}
}

// payloadProbe is a best-effort decoder that extracts the common
// identifier fields from any project payload. We deliberately limit
// the probe to field names that have a consistent meaning across every
// existing payload type:
//
//   - tenant_id          — uniformly the tenant uint64
//   - knowledge_base_id  — wiki / chunk / image / post-process / ...
//   - kb_id              — FAQImportPayload's alias for the same thing
//   - knowledge_id       — per-document scope
//   - source_kb_id       — KnowledgeMovePayload (semantic: source KB)
//
// Other plausible-looking keys (`source_id`, `target_id`) are NOT
// probed: different payloads use them with different meanings (KB
// clone vs data-source sync), and a wrong scope inference would route
// dead letters under a misleading bucket. New payloads that need a
// specific scope should reuse one of the canonical keys above.
//
// Missing keys decode to zero values, which we then fall back through
// in inferScope.
type payloadProbe struct {
	TenantID        uint64 `json:"tenant_id,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	KBID            string `json:"kb_id,omitempty"` // FAQImportPayload uses this name
	KnowledgeID     string `json:"knowledge_id,omitempty"`
	SourceKBID      string `json:"source_kb_id,omitempty"` // KnowledgeMovePayload
}

// inferScope picks the most-specific scope tuple available in the
// probe. The order reflects the conventional "blast radius" of a
// task: knowledge_base is broader than knowledge, etc. Wiki ingest is
// scope="knowledge_base"; chunk extract is scope="knowledge"; and so on.
//
// Falls back to scope="unknown" with empty scope_id if the payload
// carries no recognizable identifier — better than silently dropping
// the row.
func inferScope(p payloadProbe) (string, string) {
	switch {
	case p.KnowledgeBaseID != "":
		return types.TaskScopeKnowledgeBase, p.KnowledgeBaseID
	case p.KBID != "":
		return types.TaskScopeKnowledgeBase, p.KBID
	case p.SourceKBID != "":
		// Knowledge move spans two KBs; the source is the better
		// blast-radius indicator (it's where the work was being read
		// from when the failure occurred).
		return types.TaskScopeKnowledgeBase, p.SourceKBID
	case p.KnowledgeID != "":
		return types.TaskScopeKnowledge, p.KnowledgeID
	case p.TenantID != 0:
		return types.TaskScopeTenant, formatUint(p.TenantID)
	default:
		return types.TaskScopeUnknown, ""
	}
}

// truncateError prevents a single runaway stack trace from inflating
// the dead-letter table beyond the disk capacity reasonably allotted
// to it.
func truncateError(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	const suffix = "...(truncated)"
	if max <= len(suffix) {
		return s[:max]
	}
	return s[:max-len(suffix)] + suffix
}

// formatUint inlines strconv.FormatUint without importing strconv just
// for one call (keeps the package import set minimal).
func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
