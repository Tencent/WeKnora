package utils

import (
	"testing"
	"time"
)

// TestOutboundDialTimeoutDefault verifies the package default is 5s.
// Keep these tests serial: they read and mutate the same process-wide timeout.
func TestOutboundDialTimeoutDefault(t *testing.T) {
	if got := OutboundDialTimeout(); got != 5*time.Second {
		t.Fatalf("default dial timeout = %v, want %v", got, 5*time.Second)
	}
}

// TestSetOutboundDialTimeout verifies the setter is reflected by the getter and
// that non-positive values are ignored.
func TestSetOutboundDialTimeout(t *testing.T) {
	original := OutboundDialTimeout()
	t.Cleanup(func() { SetOutboundDialTimeout(original) })

	SetOutboundDialTimeout(2 * time.Second)
	if got := OutboundDialTimeout(); got != 2*time.Second {
		t.Fatalf("after set 2s: got %v, want %v", got, 2*time.Second)
	}

	// Non-positive must be ignored, leaving the previous value intact.
	SetOutboundDialTimeout(0)
	if got := OutboundDialTimeout(); got != 2*time.Second {
		t.Fatalf("after set 0: got %v, want %v (value should be unchanged)", got, 2*time.Second)
	}
}
