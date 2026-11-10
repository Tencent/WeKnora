package runtime

import (
	"testing"
	"time"
)

func TestMarkServerStartedAndUptime(t *testing.T) {
	MarkServerStarted()
	boot := ServerStartedAt()
	if boot.IsZero() {
		t.Fatal("ServerStartedAt should be set after MarkServerStarted")
	}
	time.Sleep(2 * time.Millisecond)
	if got := ServerUptime(); got <= 0 {
		t.Fatalf("ServerUptime() = %v, want > 0", got)
	}
}
