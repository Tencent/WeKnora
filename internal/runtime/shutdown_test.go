package runtime

import (
	"testing"
	"time"
)

func TestShutdownBudgetsShareOneDeadline(t *testing.T) {
	tests := []struct {
		name        string
		total       time.Duration
		wantDrain   time.Duration
		wantCleanup time.Duration
	}{
		{name: "unset", total: 0, wantDrain: 25 * time.Second, wantCleanup: 5 * time.Second},
		{name: "default", total: 30 * time.Second, wantDrain: 25 * time.Second, wantCleanup: 5 * time.Second},
		{name: "fits reserve", total: 10 * time.Second, wantDrain: 5 * time.Second, wantCleanup: 5 * time.Second},
		{name: "split small", total: 4 * time.Second, wantDrain: 2 * time.Second, wantCleanup: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drain, cleanup := ShutdownBudgets(tt.total)
			if drain != tt.wantDrain || cleanup != tt.wantCleanup {
				t.Fatalf("ShutdownBudgets(%s) = %s drain + %s cleanup, want %s + %s",
					tt.total, drain, cleanup, tt.wantDrain, tt.wantCleanup)
			}
			total := tt.total
			if total <= 0 {
				total = DefaultShutdownTimeout
			}
			if drain+cleanup != total {
				t.Fatalf("budgets sum to %s, want %s", drain+cleanup, total)
			}
		})
	}
}

func TestWaitFor(t *testing.T) {
	closed := make(chan struct{})
	close(closed)
	if !WaitFor(closed, time.Millisecond) {
		t.Fatal("closed channel should be received")
	}

	never := make(chan struct{})
	start := time.Now()
	if WaitFor(never, 20*time.Millisecond) {
		t.Fatal("timeout should lose")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("timeout waited %s", elapsed)
	}

	later := make(chan struct{})
	go func() {
		time.Sleep(15 * time.Millisecond)
		close(later)
	}()
	if !WaitFor(later, 0) {
		t.Fatal("non-positive timeout should wait for the channel")
	}
}
