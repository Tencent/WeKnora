package types

import "testing"

func TestNormalizeAgentSourceTenantID(t *testing.T) {
	if got := NormalizeAgentSourceTenantID(0, 7); got != 0 {
		t.Fatalf("absent selector must stay absent, got %d", got)
	}
	if got := NormalizeAgentSourceTenantID(7, 7); got != 0 {
		t.Fatalf("self selector must normalize to absent, got %d", got)
	}
	if got := NormalizeAgentSourceTenantID(84, 7); got != 84 {
		t.Fatalf("foreign selector must pass through, got %d", got)
	}
}
