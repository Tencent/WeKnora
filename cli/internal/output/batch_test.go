package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/output"
)

func TestWriteBatchEnvelope_AllSuccess(t *testing.T) {
	items := []output.BatchItem{
		{ID: "id1", OK: true, Result: map[string]string{"deleted_at": "2026-05-20T00:00:00Z"}},
		{ID: "id2", OK: true, Result: map[string]string{"deleted_at": "2026-05-20T00:00:00Z"}},
	}
	var buf bytes.Buffer
	if err := output.WriteBatchEnvelope(&buf, items, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("expected ok:true; got %q", got)
	}
	if !strings.Contains(got, `"successes":2`) {
		t.Errorf("expected successes:2; got %q", got)
	}
	if !strings.Contains(got, `"count":2`) {
		t.Errorf("expected count:2; got %q", got)
	}
}

func TestWriteBatchEnvelope_PartialFailure(t *testing.T) {
	items := []output.BatchItem{
		{ID: "id1", OK: true, Result: map[string]string{"deleted_at": "2026-05-20T00:00:00Z"}},
		{ID: "id2", OK: false, Error: &output.ErrDetail{Type: "resource.not_found", Message: "id2 not found"}},
	}
	var buf bytes.Buffer
	if err := output.WriteBatchEnvelope(&buf, items, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"ok":false`) {
		t.Errorf("expected ok:false (partial failure); got %q", got)
	}
	if !strings.Contains(got, `"successes":1`) || !strings.Contains(got, `"failures":1`) {
		t.Errorf("expected counts; got %q", got)
	}
	if !strings.Contains(got, `"type":"resource.not_found"`) {
		t.Errorf("expected per-item error; got %q", got)
	}
}

func TestWriteBatchEnvelope_EmptyItems_EmitsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	if err := output.WriteBatchEnvelope(&buf, nil, false, ""); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `"data":null`) {
		t.Errorf("empty items must marshal to [], not null; got %q", got)
	}
	if !strings.Contains(got, `"data":[]`) {
		t.Errorf("expected data:[]; got %q", got)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("expected ok:true for empty batch; got %q", got)
	}
	// count:0 is omitted by omitempty; assert no non-zero count appears.
	if strings.Contains(got, `"count":`) {
		// If count key is present, it must not be a non-zero value.
		if !strings.Contains(got, `"count":0`) {
			t.Errorf("count key present but not zero; got %q", got)
		}
	}
}

func TestWriteBatchEnvelope_AllFail(t *testing.T) {
	items := []output.BatchItem{
		{ID: "id1", OK: false, Error: &output.ErrDetail{Type: "auth.forbidden", Message: "no perm"}},
		{ID: "id2", OK: false, Error: &output.ErrDetail{Type: "resource.not_found", Message: "missing"}},
	}
	var buf bytes.Buffer
	if err := output.WriteBatchEnvelope(&buf, items, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `"ok":false`) {
		t.Errorf("expected ok:false; got %q", got)
	}
	// All-fail still uses the batch envelope shape; it does not degrade
	// to ErrorEnvelope.
	// An ErrorEnvelope has a top-level "error" key alongside "ok"; a batch
	// envelope has a top-level "data" array. Check that "data":[  is present.
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("batch all-fail should still use batch shape (data array), not error envelope; got %q", got)
	}
	if !strings.Contains(got, `"failures":2`) {
		t.Errorf("expected failures:2; got %q", got)
	}
	// H3b invariant: successes:0 must appear even when all items failed.
	// (*int semantics: nil is omitted; &0 is serialized as 0.)
	if !strings.Contains(got, `"successes":0`) {
		t.Errorf("all-fail batch must emit successes:0 (not omit it); invariant successes+failures==count would be broken; got %q", got)
	}
}
