package anydoc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The fake in resource_limit_test.go reports resource_limit only; extend it
// here with the encrypted kind so the default build can classify both.
type fakeKindError struct {
	kind   string
	detail string
}

func (e *fakeKindError) Error() string {
	if e.detail == "" {
		return "anydoc: " + e.kind
	}
	return "anydoc: " + e.kind + ": " + e.detail
}

func (e *fakeKindError) ResourceLimit() bool { return e.kind == "resource_limit" }

func (e *fakeKindError) LimitDetail() string { return e.detail }

func (e *fakeKindError) Encrypted() bool { return e.kind == "encrypted" }

func TestMarkEncryptedClassifiesPasswordProtectedRejection(t *testing.T) {
	raw := &fakeKindError{kind: "encrypted", detail: "document is encrypted"}
	backendErr := fmt.Errorf("anydoc: markdown conversion failed: %w", raw)

	got := markEncrypted(backendErr)
	if !errors.Is(got, ErrEncrypted) {
		t.Fatalf("markEncrypted() = %v, want errors.Is ErrEncrypted", got)
	}
	// The reader keeps the chain with %w, which is what convert inspects.
	readerErr := fmt.Errorf("anydoc conversion failed for %q: %w", "protected.xlsx", got)
	if !errors.Is(readerErr, ErrEncrypted) {
		t.Fatalf("reader wrap = %v, want errors.Is ErrEncrypted", readerErr)
	}
	// The wrapper's own message names the reason; the reader chain keeps
	// the file name prefix around it.
	if got := readerErr.Error(); !strings.Contains(got, "document is encrypted; upload a copy without a password") {
		t.Errorf("message = %q, want it to name the encrypted rejection", got)
	}
}

func TestMarkEncryptedLeavesOtherKindsUnwrapped(t *testing.T) {
	raw := &fakeKindError{kind: "io", detail: "read failed"}
	backendErr := fmt.Errorf("anydoc: markdown conversion failed: %w", raw)

	got := markEncrypted(backendErr)
	if errors.Is(got, ErrEncrypted) {
		t.Fatalf("markEncrypted() classified an io error as encrypted: %v", got)
	}
	if got != backendErr {
		t.Errorf("markEncrypted() = %v, want the original error unchanged", got)
	}
}
