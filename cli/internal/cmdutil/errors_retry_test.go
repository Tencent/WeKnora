package cmdutil

import (
	"reflect"
	"testing"
)

func TestErrorToDetail_NilSafe(t *testing.T) {
	if got := ErrorToDetail(nil); got != nil {
		t.Errorf("ErrorToDetail(nil) should return nil; got %v", got)
	}
}

func TestError_WithRetryArgv(t *testing.T) {
	err := NewError(CodeAuthUnauthenticated, "session expired").
		WithHint("run `weknora auth login`").
		WithRetryArgv([]string{"weknora", "auth", "login"})

	if !reflect.DeepEqual(err.RetryArgv, []string{"weknora", "auth", "login"}) {
		t.Errorf("RetryArgv not set; got %v", err.RetryArgv)
	}
	if err.Hint != "run `weknora auth login`" {
		t.Errorf("Hint changed unexpectedly; got %q", err.Hint)
	}
}

func TestError_RetryArgv_EmptyByDefault(t *testing.T) {
	err := NewError(CodeResourceAlreadyExists, "kb name exists")
	if len(err.RetryArgv) != 0 {
		t.Errorf("RetryArgv should default empty; got %v", err.RetryArgv)
	}
}
