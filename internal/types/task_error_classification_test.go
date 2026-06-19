package types

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

type temporaryNetError struct{}

func (temporaryNetError) Error() string   { return "temporary network failure" }
func (temporaryNetError) Timeout() bool   { return false }
func (temporaryNetError) Temporary() bool { return true }

var _ net.Error = temporaryNetError{}

func TestClassifyTaskError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want TaskErrorClass
	}{
		{"canceled", context.Canceled, TaskErrorClassCanceled},
		{"deadline", context.DeadlineExceeded, TaskErrorClassRetryable},
		{"rate limit", &ProviderHTTPError{Status: 429}, TaskErrorClassRetryable},
		{"auth", &ProviderHTTPError{Status: 401}, TaskErrorClassTerminal},
		{"server", &ProviderHTTPError{Status: 503}, TaskErrorClassRetryable},
		{"temporary network", temporaryNetError{}, TaskErrorClassRetryable},
		{"validation", errors.New("bad payload"), TaskErrorClassTerminal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClassifyTaskError(tt.err))
		})
	}
}
