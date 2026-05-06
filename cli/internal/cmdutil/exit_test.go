package cmdutil

import (
	"bytes"
	"errors"
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExitCode(tc.err))
		})
	}
}

func TestToErrorBody(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		assert.Nil(t, ToErrorBody(nil))
	})
	t.Run("typed without cause", func(t *testing.T) {
		body := ToErrorBody(NewError(CodeResourceNotFound, "kb missing"))
		assert.Equal(t, "resource.not_found", body.Code)
		assert.Equal(t, "kb missing", body.Message)
	})
	t.Run("typed with cause surfaces inner", func(t *testing.T) {
		inner := errors.New("HTTP error 500: server exploded")
		body := ToErrorBody(Wrapf(CodeServerError, inner, "hybrid search"))
		assert.Equal(t, "server.error", body.Code)
		// Both wrap context AND server cause must appear so agents see what
		// actually broke, not just our wrap label.
		assert.Equal(t, "hybrid search: HTTP error 500: server exploded", body.Message)
	})
	t.Run("flag error", func(t *testing.T) {
		body := ToErrorBody(NewFlagError(errors.New("bad flag")))
		assert.Equal(t, "input.invalid_argument", body.Code)
	})
	t.Run("unclassified", func(t *testing.T) {
		body := ToErrorBody(errors.New("anything"))
		assert.Equal(t, "server.error", body.Code)
		assert.Equal(t, "anything", body.Message)
	})
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
