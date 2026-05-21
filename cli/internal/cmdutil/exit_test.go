package cmdutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil success", nil, 0},
		{"flag error", NewFlagError(errors.New("bad flag")), 2},
		{"silent", SilentError, 1},
		{"auth.* prefix", NewError(CodeAuthUnauthenticated, "x"), 3},
		{"auth.token_expired", NewError(CodeAuthTokenExpired, "x"), 3},
		{"resource.not_found", NewError(CodeResourceNotFound, "x"), 4},
		{"input.* prefix", NewError(CodeInputInvalidArgument, "x"), 5},
		{"input.missing_flag", NewError(CodeInputMissingFlag, "x"), 5},
		{"server.rate_limited", NewError(CodeServerRateLimited, "x"), 6},
		{"server.* prefix", NewError(CodeServerError, "x"), 7},
		{"server.timeout", NewError(CodeServerTimeout, "x"), 7},
		{"network.* prefix", NewError(CodeNetworkError, "x"), 7},
		{"unknown error", errors.New("plain"), 1},
		{"local.* prefix", NewError(CodeLocalConfigCorrupt, "x"), 1},
		{"operation.timeout", NewError(CodeOperationTimeout, "timed out"), 124},
		{"operation.failed → 1 (fall-through bucket)", NewError(CodeOperationFailed, "failed"), 1},
		{"operation.cancelled → 1 (main overrides to 130 on signal-cancelled ctx)", NewError(CodeOperationCancelled, "cancelled"), 1},
		{"server.session_create_failed → 1 (workflow, not transient)", NewError(CodeSessionCreateFailed, "x"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExitCode(tc.err))
		})
	}
}

func TestPrintError(t *testing.T) {
	t.Run("nil is silent", func(t *testing.T) {
		var buf bytes.Buffer
		PrintError(&buf, nil)
		assert.Empty(t, buf.String())
	})
	t.Run("SilentError is silent", func(t *testing.T) {
		var buf bytes.Buffer
		PrintError(&buf, SilentError)
		assert.Empty(t, buf.String())
	})
	t.Run("typed error prints message", func(t *testing.T) {
		var buf bytes.Buffer
		PrintError(&buf, NewError(CodeAuthUnauthenticated, "no creds"))
		assert.Contains(t, buf.String(), "auth.unauthenticated")
		assert.Contains(t, buf.String(), "no creds")
	})
}

func TestPrintError_JSONMode_WritesEnvelope(t *testing.T) {
	t.Cleanup(func() { SetFormatMode("") })
	SetFormatMode("json")

	err := NewError(CodeInputConfirmationRequired, "kb delete kb_x requires confirmation").
		WithHint("re-run with -y/--yes").
		WithRetryCommand("weknora kb delete kb_x -y")

	var buf bytes.Buffer
	PrintError(&buf, err)

	got := buf.String()
	if !strings.Contains(got, `"ok":false`) {
		t.Errorf("expected envelope ok:false; got %q", got)
	}
	if !strings.Contains(got, `"type":"input.confirmation_required"`) {
		t.Errorf("expected typed code; got %q", got)
	}
	if !strings.Contains(got, `"retry_command":"weknora kb delete kb_x -y"`) {
		t.Errorf("expected retry_command; got %q", got)
	}
}

func TestPrintError_JSONMode_IncludesRetryAfter(t *testing.T) {
	t.Cleanup(func() { SetFormatMode("") })
	SetFormatMode("json")

	err := NewError(CodeServerRateLimited, "rate limited").
		WithRetryAfter(30)

	var buf bytes.Buffer
	PrintError(&buf, err)

	got := buf.String()
	if !strings.Contains(got, `"retry_after_seconds":30`) {
		t.Errorf("expected retry_after_seconds:30; got %q", got)
	}
}

func TestPrintError_TextMode_WritesProse(t *testing.T) {
	t.Cleanup(func() { SetFormatMode("") })
	SetFormatMode("text")

	err := NewError(CodeInputConfirmationRequired, "kb delete kb_x requires confirmation").
		WithHint("re-run with -y/--yes").
		WithRetryCommand("weknora kb delete kb_x -y")

	var buf bytes.Buffer
	PrintError(&buf, err)

	got := buf.String()
	if !strings.Contains(got, "input.confirmation_required: kb delete kb_x requires confirmation") {
		t.Errorf("expected prose code:message line; got %q", got)
	}
	if !strings.Contains(got, "hint: re-run with -y/--yes") {
		t.Errorf("expected hint line; got %q", got)
	}
	if !strings.Contains(got, "retry: weknora kb delete kb_x -y") {
		t.Errorf("expected retry line; got %q", got)
	}
}
