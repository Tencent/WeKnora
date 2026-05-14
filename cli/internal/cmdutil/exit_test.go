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
