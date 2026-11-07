package anydoc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeConvertError stands in for the cgo binding's *ConvertError. The
// production type exposes the same two methods; this fake lets the default
// build classify a resource_limit failure without linking the parser.
type fakeConvertError struct {
	kind   string
	detail string
}

func (e *fakeConvertError) Error() string {
	if e.detail == "" {
		return "anydoc: " + e.kind
	}
	return "anydoc: " + e.kind + ": " + e.detail
}

func (e *fakeConvertError) ResourceLimit() bool { return e.kind == "resource_limit" }

func (e *fakeConvertError) LimitDetail() string { return e.detail }

func TestMarkResourceLimitClassifiesPermanentBudgetFailure(t *testing.T) {
	const detail = "resource limit exceeded (max_xml_nodes): part exceeds 2000000 xml nodes"
	raw := &fakeConvertError{kind: "resource_limit", detail: detail}
	// Same wrap the cgo backend applies before the reader names the file.
	backendErr := fmt.Errorf("anydoc: markdown conversion failed: %w", raw)

	got := markResourceLimit(backendErr)
	if !errors.Is(got, ErrResourceLimit) {
		t.Fatalf("markResourceLimit() = %v, want errors.Is ErrResourceLimit", got)
	}
	// The reader keeps the chain with %w, which is what convert inspects.
	readerErr := fmt.Errorf("anydoc conversion failed for %q: %w", "xlsx50k.xlsx", got)
	if !errors.Is(readerErr, ErrResourceLimit) {
		t.Fatalf("reader wrap = %v, want errors.Is ErrResourceLimit", readerErr)
	}
	message := readerErr.Error()
	for _, want := range []string{"too large", "max_xml_nodes", "2000000"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not mention %q", message, want)
		}
	}
	if strings.Contains(message, "DocReader") {
		t.Errorf("message points at DocReader, which did not parse the file: %q", message)
	}
	if !errors.Is(got, raw) {
		t.Fatal("resource-limit wrap dropped the converter error")
	}
}

func TestMarkResourceLimitLeavesOtherFailuresRetryable(t *testing.T) {
	malformed := &fakeConvertError{kind: "malformed", detail: "not a readable zip archive"}
	in := fmt.Errorf("anydoc: markdown conversion failed: %w", malformed)
	if got := markResourceLimit(in); got != in || errors.Is(got, ErrResourceLimit) {
		t.Fatalf("malformed error = %v, want it unchanged and retryable", got)
	}

	// The kind has to be typed. A matching sentence from somewhere else is
	// not a permanent parser rejection.
	untyped := errors.New("anydoc: resource_limit: resource limit exceeded (max_xml_nodes): " +
		"part exceeds 2000000 xml nodes")
	if got := markResourceLimit(untyped); errors.Is(got, ErrResourceLimit) {
		t.Fatalf("untyped error was classified as a resource limit: %v", got)
	}

	if got := markResourceLimit(nil); got != nil {
		t.Fatalf("markResourceLimit(nil) = %v, want nil", got)
	}

	bare := &fakeConvertError{kind: "resource_limit"}
	got := markResourceLimit(bare)
	if !errors.Is(got, ErrResourceLimit) {
		t.Fatalf("limit without detail = %v, want ErrResourceLimit", got)
	}
	if !strings.Contains(got.Error(), "too large") {
		t.Fatalf("limit without detail = %q, want the permanent-failure reason", got.Error())
	}
}
