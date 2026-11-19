package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"
)

func TestIsToolCallRetrySafe(t *testing.T) {
	tests := []struct {
		name string
		err  error
		safe bool
	}{
		{name: "nil"},
		{name: "not_connected", err: fmt.Errorf("call: %w", ErrNotConnected), safe: true},
		{
			name: "expired_session",
			err:  fmt.Errorf("call: %w", transport.NewError(transport.ErrSessionTerminated)),
			safe: true,
		},
		{name: "timeout", err: transport.NewError(context.DeadlineExceeded)},
		{name: "cancel", err: fmt.Errorf("call: %w", context.Canceled)},
		{name: "eof", err: errors.New("unexpected EOF")},
		{name: "session_text", err: transport.NewError(errors.New("Invalid session ID"))},
		{name: "authorization_required", err: &transport.AuthorizationRequiredError{}, safe: true},
		{name: "oauth_required", err: &transport.OAuthAuthorizationRequiredError{}, safe: true},
		{name: "reauthorization_required", err: &OAuthReauthorizationRequiredError{}, safe: true},
		{name: "unknown", err: errors.Join(ErrToolCallOutcomeUnknown, ErrNotConnected)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.safe, IsToolCallRetrySafe(tt.err))
		})
	}
}
