package client

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsSSEStreamError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"typed", NewSSEStreamError("boom"), true},
		{"wrapped", fmt.Errorf("request failed: %w", NewSSEStreamError("boom")), true},
		{"legacy_string", fmt.Errorf("SSE stream error: boom"), true},
		{"http", fmt.Errorf("HTTP error 500: internal"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSSEStreamError(tc.err); got != tc.want {
				t.Errorf("IsSSEStreamError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSSEStreamError_Unwrap(t *testing.T) {
	err := NewSSEStreamError("boom")
	if !errors.Is(err, ErrSSEStreamTerminal) {
		t.Fatal("expected ErrSSEStreamTerminal in chain")
	}
}
