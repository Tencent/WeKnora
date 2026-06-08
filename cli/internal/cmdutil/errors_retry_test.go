package cmdutil

import (
	"testing"
)

func TestErrorToDetail_NilSafe(t *testing.T) {
	if got := ErrorToDetail(nil); got != nil {
		t.Errorf("ErrorToDetail(nil) should return nil; got %v", got)
	}
}

func TestError_WithRetryCommand(t *testing.T) {
	err := NewError(CodeAuthUnauthenticated, "session expired").
		WithHint("run `weknora auth login`").
		WithRetryCommand("weknora auth login")

	if err.RetryCommand != "weknora auth login" {
		t.Errorf("RetryCommand not set; got %q", err.RetryCommand)
	}
	if err.Hint != "run `weknora auth login`" {
		t.Errorf("Hint changed unexpectedly; got %q", err.Hint)
	}
}

func TestError_RetryCommand_EmptyByDefault(t *testing.T) {
	err := NewError(CodeResourceAlreadyExists, "kb name exists")
	if err.RetryCommand != "" {
		t.Errorf("RetryCommand should default empty; got %q", err.RetryCommand)
	}
}
