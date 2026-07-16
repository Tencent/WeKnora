package cmdutil

import (
	"errors"
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

// TestErrorToDetail_CarriesExitCode verifies every error detail embeds the
// authoritative exit_code so an agent can branch on a single JSON read without
// observing $?. Regression: `input.invalid_argument` spans exit 2 (parse) and
// exit 5 (typed value), so `type` alone was insufficient — exit_code
// disambiguates them in the envelope.
func TestErrorToDetail_CarriesExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not_found", NewError(CodeResourceNotFound, "x"), 4},
		{"typed_input_value", NewError(CodeInputInvalidArgument, "bad value"), 5},
		{"auth", NewError(CodeAuthUnauthenticated, "x"), 3},
		{"parse_flagerror", NewFlagError(errors.New("unknown flag: --nope")), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ErrorToDetail(tc.err)
			if d == nil {
				t.Fatal("nil detail")
			}
			if d.ExitCode != tc.want {
				t.Errorf("exit_code = %d, want %d (type=%s)", d.ExitCode, tc.want, d.Type)
			}
		})
	}
}
