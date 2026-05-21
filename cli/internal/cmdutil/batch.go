// Package cmdutil — batch.go provides reusable plumbing for multi-target
// mutations that emit the §4.5 batch envelope.
//
// Three pieces:
//   - BatchOutcome: per-target structured outcome (preserves argv order).
//   - RunBatch: drives a slice of targets through a closure, collecting
//     outcomes. Stops on context cancellation; keep-going on per-item err.
//   - EmitBatch: renders the outcomes — JSON/NDJSON via output.WriteBatchEnvelope,
//     text via per-item "OK <id>" / "FAIL <id>: <msg>".

package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/output"
)

// BatchOutcome is one per-target result preserving argv order. Err==nil ⇒ success.
type BatchOutcome struct {
	ID  string
	Err error
}

// RunBatch invokes op(ctx, id) for each id and collects outcomes in order.
// Per-item errors do not abort the run; context cancellation does (the
// remainder of the slice is marked with the context error).
//
// Returns (outcomes, summaryErr). summaryErr is a typed *Error with
// CodeOperationFailed when any per-item op failed, nil otherwise. Callers
// emit the outcomes before returning summaryErr so partial-success data
// reaches stdout.
func RunBatch(ctx context.Context, ids []string, op func(context.Context, string) error) ([]BatchOutcome, error) {
	outcomes := make([]BatchOutcome, 0, len(ids))
	failed := 0
	for _, id := range ids {
		select {
		case <-ctx.Done():
			outcomes = append(outcomes, BatchOutcome{ID: id, Err: ctx.Err()})
			failed++
			continue
		default:
		}
		err := op(ctx, id)
		outcomes = append(outcomes, BatchOutcome{ID: id, Err: err})
		if err != nil {
			failed++
		}
	}
	if failed > 0 {
		return outcomes, &Error{
			Code:    CodeOperationFailed,
			Message: fmt.Sprintf("%d/%d operation(s) failed", failed, len(ids)),
			// Silent suppresses the stderr error envelope because the caller
			// already emitted the batch envelope to stdout (§4.5). The exit code
			// still propagates via Error.Code → ExitCode (falls through to 1).
			Silent: true,
		}
	}
	return outcomes, nil
}

// EmitBatch writes the per-item outcomes per --format. JSON/NDJSON emit
// the §4.5 batch envelope; text mode emits per-line "OK <id>" /
// "FAIL <id>: <msg>".
//
// resultFn builds the per-item Result map for successes; nil ⇒ omit.
// A typical resultFn returns map[string]any{"deleted_at": time.Now()....}.
//
// Callers wanting a stable per-item timestamp pass time.Now() via
// resultFn so tests can pin the clock with SetDeletedAtClock.
func EmitBatch(outcomes []BatchOutcome, fopts *FormatOptions, w io.Writer, resultFn func(id string) any) error {
	if fopts.WantsJSON() {
		items := make([]output.BatchItem, len(outcomes))
		for i, o := range outcomes {
			items[i] = output.BatchItem{ID: o.ID, OK: o.Err == nil}
			if o.Err != nil {
				items[i].Error = ErrorToDetail(o.Err)
			} else if resultFn != nil {
				items[i].Result = resultFn(o.ID)
			}
		}
		return output.WriteBatchEnvelope(w, items, fopts.TTY, globalProfile)
	}
	for _, o := range outcomes {
		if o.Err == nil {
			fmt.Fprintf(w, "OK %s\n", o.ID)
			continue
		}
		fmt.Fprintf(w, "FAIL %s: %s\n", o.ID, o.Err)
	}
	return nil
}

// deletedAtClock is the clock source for DeletedAtNow. Overridable in tests
// to make per-item timestamps deterministic.
var deletedAtClock = time.Now

// DeletedAtNow is the canonical resultFn for delete batches that report
// {deleted_at: <RFC3339>} per success. Tests can freeze the clock via
// SetDeletedAtClock(func() time.Time { return fixed }).
func DeletedAtNow(id string) any {
	return map[string]any{"deleted_at": deletedAtClock().Format(time.RFC3339)}
}

// SetDeletedAtClock overrides the time source used by DeletedAtNow. Returns
// a cleanup func that restores time.Now. For test use only.
func SetDeletedAtClock(clock func() time.Time) func() {
	deletedAtClock = clock
	return func() { deletedAtClock = time.Now }
}

// ClassifyContextErr maps a context error to the appropriate operation code.
// Falls back to CodeOperationFailed when err is nil or not a context signal.
func ClassifyContextErr(err error) ErrorCode {
	switch {
	case errors.Is(err, context.Canceled):
		return CodeOperationCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return CodeOperationTimeout
	default:
		return CodeOperationFailed
	}
}
