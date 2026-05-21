package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRunBatch_AllSuccess verifies that 3 ids all succeed: outcomes are
// ordered and summaryErr is nil.
func TestRunBatch_AllSuccess(t *testing.T) {
	ids := []string{"a", "b", "c"}
	op := func(_ context.Context, id string) error { return nil }

	outcomes, err := RunBatch(context.Background(), ids, op)
	if err != nil {
		t.Fatalf("expected nil summaryErr; got %v", err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("expected 3 outcomes; got %d", len(outcomes))
	}
	for i, o := range outcomes {
		if o.ID != ids[i] {
			t.Errorf("outcomes[%d].ID = %q, want %q", i, o.ID, ids[i])
		}
		if o.Err != nil {
			t.Errorf("outcomes[%d].Err = %v, want nil", i, o.Err)
		}
	}
}

// TestRunBatch_PartialFailure verifies that one failing id yields summaryErr
// with CodeOperationFailed while successful outcomes are preserved.
func TestRunBatch_PartialFailure(t *testing.T) {
	ids := []string{"ok1", "fail", "ok2"}
	errFail := errors.New("something went wrong")
	op := func(_ context.Context, id string) error {
		if id == "fail" {
			return errFail
		}
		return nil
	}

	outcomes, summaryErr := RunBatch(context.Background(), ids, op)
	if summaryErr == nil {
		t.Fatal("expected non-nil summaryErr")
	}
	typedErr := AsError(summaryErr)
	if typedErr == nil {
		t.Fatalf("summaryErr is not *Error; got %T %v", summaryErr, summaryErr)
	}
	if typedErr.Code != CodeOperationFailed {
		t.Errorf("summaryErr.Code = %q, want %q", typedErr.Code, CodeOperationFailed)
	}
	if !strings.Contains(typedErr.Message, "1/3") {
		t.Errorf("summaryErr.Message = %q, expected 1/3 ratio", typedErr.Message)
	}
	if len(outcomes) != 3 {
		t.Fatalf("expected 3 outcomes; got %d", len(outcomes))
	}
	if outcomes[1].Err != errFail {
		t.Errorf("outcomes[1].Err = %v, want %v", outcomes[1].Err, errFail)
	}
	if outcomes[0].Err != nil || outcomes[2].Err != nil {
		t.Error("expected outcomes[0] and outcomes[2] to have nil Err")
	}
}

// TestRunBatch_ContextCancellation verifies that once the context is cancelled,
// remaining ids are marked with the context error without calling op.
func TestRunBatch_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	ids := []string{"first", "second", "third"}
	opCalled := 0
	op := func(ctx context.Context, id string) error {
		opCalled++
		if id == "first" {
			cancel() // cancel after the first item
		}
		return nil
	}

	outcomes, summaryErr := RunBatch(ctx, ids, op)

	// summaryErr must be non-nil (cancelled items counted as failed)
	if summaryErr == nil {
		t.Fatal("expected non-nil summaryErr due to cancellation")
	}
	if len(outcomes) != 3 {
		t.Fatalf("expected 3 outcomes; got %d", len(outcomes))
	}
	// "second" and "third" should have ctx.Err() as their error
	for _, id := range []string{"second", "third"} {
		var found *BatchOutcome
		for i := range outcomes {
			if outcomes[i].ID == id {
				found = &outcomes[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("missing outcome for id %q", id)
		}
		if !errors.Is(found.Err, context.Canceled) {
			t.Errorf("outcome[%s].Err = %v, want context.Canceled", id, found.Err)
		}
	}
}

// TestEmitBatch_JSON_Envelope verifies that the JSON path emits a valid §4.5
// batch envelope with correct ok/error/result fields.
func TestEmitBatch_JSON_Envelope(t *testing.T) {
	outcomes := []BatchOutcome{
		{ID: "x", Err: nil},
		{ID: "y", Err: NewError(CodeResourceNotFound, "not found")},
	}
	fopts := &FormatOptions{Mode: FormatJSON, TTY: false}
	var buf bytes.Buffer

	err := EmitBatch(outcomes, fopts, &buf, func(id string) any {
		return map[string]any{"deleted_at": "2026-01-01T00:00:00Z"}
	})
	if err != nil {
		t.Fatalf("EmitBatch error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("expected ok:true in envelope; got %q", got)
	}
	if !strings.Contains(got, `"id":"x"`) {
		t.Errorf("expected id:x; got %q", got)
	}
	if !strings.Contains(got, `"id":"y"`) {
		t.Errorf("expected id:y; got %q", got)
	}
	if !strings.Contains(got, `"deleted_at":"2026-01-01T00:00:00Z"`) {
		t.Errorf("expected result.deleted_at for x; got %q", got)
	}
	if !strings.Contains(got, `"type":"resource.not_found"`) {
		t.Errorf("expected error.type for y; got %q", got)
	}
	// meta.failures should be 1
	if !strings.Contains(got, `"failures":1`) {
		t.Errorf("expected meta.failures:1; got %q", got)
	}
}

// TestDeletedAtNow_FixedClock verifies that SetDeletedAtClock overrides the
// timestamp used by DeletedAtNow, making per-item values deterministic in tests.
func TestDeletedAtNow_FixedClock(t *testing.T) {
	fixed := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	defer SetDeletedAtClock(func() time.Time { return fixed })()

	got := DeletedAtNow("irrelevant")
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("DeletedAtNow returned %T, want map[string]any", got)
	}
	want := fixed.Format(time.RFC3339)
	if m["deleted_at"] != want {
		t.Errorf("deleted_at = %q, want %q", m["deleted_at"], want)
	}
}

// TestEmitBatch_Text_PerLine verifies that the human/text path emits
// "OK <id>" / "FAIL <id>: <msg>" per line.
func TestEmitBatch_Text_PerLine(t *testing.T) {
	outcomes := []BatchOutcome{
		{ID: "x", Err: nil},
		{ID: "y", Err: fmt.Errorf("boom")},
	}
	fopts := &FormatOptions{Mode: FormatHuman}
	var buf bytes.Buffer

	err := EmitBatch(outcomes, fopts, &buf, nil)
	if err != nil {
		t.Fatalf("EmitBatch error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "OK x\n") {
		t.Errorf("expected 'OK x' line; got %q", got)
	}
	if !strings.Contains(got, "FAIL y: boom\n") {
		t.Errorf("expected 'FAIL y: boom' line; got %q", got)
	}
}
