package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDocReaderFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{
			"scanned PDF wrapped connection failure",
			fmt.Errorf("anydoc fallback: %w",
				fmt.Errorf("read stream: %w",
					status.Error(codes.Unavailable,
						"private-address secret-token"))),
			werrors.ErrCodeDocReaderUnavailable,
		},
		{
			"HTTP connection failure",
			&net.OpError{
				Op:  "dial",
				Err: errors.New("private-address secret-token"),
			},
			werrors.ErrCodeDocReaderUnavailable,
		},
		{"context timeout", fmt.Errorf("parse: %w", context.DeadlineExceeded), werrors.ErrCodeDocReaderTimeout},
		{"RPC timeout", status.Error(codes.DeadlineExceeded, "private-address"), werrors.ErrCodeDocReaderTimeout},
		{"parser failure", errors.New("private-address secret-token"), werrors.ErrCodeDocReaderParseFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, message := docReaderFailure(tc.err)
			if code != tc.code || message == "" {
				t.Fatalf("got %q %q, want code %q", code, message, tc.code)
			}
			if strings.Contains(message, "private-address") || strings.Contains(message, "secret-token") {
				t.Fatal("raw transport diagnostics leaked into public message")
			}
		})
	}
}
