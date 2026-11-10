package wecom

import (
	"testing"
	"time"
)

// TestReconnectDelayOverflow guards against the bug where large attempt
// counters produced a negative time.Duration, bypassing the max-delay
// cap and causing a busy reconnect loop (e.g. "reconnecting in -1s").
func TestReconnectDelayOverflow(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{"zero attempt clamps to base", 0, defaultReconnectBaseDelay},
		{"negative attempt clamps to base", -5, defaultReconnectBaseDelay},
		{"attempt 1 = base", 1, defaultReconnectBaseDelay},
		{"attempt 2 = 2× base", 2, 2 * defaultReconnectBaseDelay},
		{"attempt 5 = 16s", 5, 16 * time.Second},
		{"attempt 6 caps at max", 6, defaultReconnectMaxDelay},
		{"attempt 100 caps at max", 100, defaultReconnectMaxDelay},
		{"attempt 4601 caps at max (regression)", 4601, defaultReconnectMaxDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconnectDelay(tt.attempt)
			if got != tt.want {
				t.Errorf("reconnectDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
			if got <= 0 {
				t.Errorf("reconnectDelay(%d) returned non-positive %v — would cause busy loop", tt.attempt, got)
			}
		})
	}
}
