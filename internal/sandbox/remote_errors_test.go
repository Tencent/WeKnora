package sandbox

import (
	"errors"
	"fmt"
	"testing"
)

func TestRemoteBindingDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		replace  bool
		preserve bool
	}{
		{name: "nil", err: nil},
		{
			name:     "not found",
			err:      NewRemoteError(SandboxTypeCube, "Get", RemoteErrorKindNotFound, "gone", nil),
			replace:  true,
			preserve: false,
		},
		{
			name:     "terminal",
			err:      NewRemoteError(SandboxTypeCube, "Get", RemoteErrorKindTerminal, "terminated", nil),
			replace:  true,
			preserve: false,
		},
		{
			name:     "authentication",
			err:      NewRemoteError(SandboxTypeCube, "Connect", RemoteErrorKindAuthentication, "denied", nil),
			preserve: true,
		},
		{
			name:     "invalid request",
			err:      NewRemoteError(SandboxTypeCube, "Create", RemoteErrorKindInvalidRequest, "bad template", nil),
			preserve: true,
		},
		{
			name:     "unsupported",
			err:      NewRemoteError(SandboxTypeCube, "Connect", RemoteErrorKindUnsupported, "no reconnect", nil),
			preserve: true,
		},
		{
			name:     "conflict",
			err:      NewRemoteError(SandboxTypeCube, "Delete", RemoteErrorKindConflict, "busy", nil),
			preserve: true,
		},
		{
			name:     "capacity",
			err:      NewRemoteError(SandboxTypeCube, "Create", RemoteErrorKindCapacity, "full", nil),
			preserve: true,
		},
		{
			name:     "timeout",
			err:      NewRemoteError(SandboxTypeCube, "Get", RemoteErrorKindTimeout, "deadline", nil),
			preserve: true,
		},
		{
			name:     "unavailable",
			err:      NewRemoteError(SandboxTypeCube, "Health", RemoteErrorKindUnavailable, "offline", nil),
			preserve: true,
		},
		{
			name:     "internal",
			err:      NewRemoteError(SandboxTypeCube, "List", RemoteErrorKindInternal, "unknown", nil),
			preserve: true,
		},
		{
			name:     "wrapped terminal",
			err:      fmt.Errorf("probe failed: %w", NewRemoteError(SandboxTypeCube, "Get", RemoteErrorKindTerminal, "dead", nil)),
			replace:  true,
			preserve: false,
		},
		{
			name:     "unclassified defaults to preserve",
			err:      errors.New("provider returned an unknown error"),
			preserve: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CanReplaceRemoteBinding(tt.err); got != tt.replace {
				t.Fatalf("CanReplaceRemoteBinding() = %v, want %v", got, tt.replace)
			}
		})
	}
}

func TestRemoteErrorRetainsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection reset")
	err := NewRemoteError(
		SandboxTypeCube,
		"Connect",
		RemoteErrorKindUnavailable,
		"control plane unavailable",
		cause,
	)

	if !errors.Is(err, cause) {
		t.Fatal("RemoteError must retain its provider-native cause")
	}
	if got, want := err.Error(), "cube Connect: unavailable: control plane unavailable: connection reset"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
