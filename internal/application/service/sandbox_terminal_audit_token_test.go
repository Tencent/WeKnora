package service

import "testing"

func TestSandboxTerminalAuditTokenIsStableAndSessionBound(t *testing.T) {
	t.Setenv("JWT_SECRET", "terminal-audit-token-test-secret")
	first := SandboxTerminalAuditToken(7, "session-a")
	if first == "" || first != SandboxTerminalAuditToken(7, "session-a") {
		t.Fatalf("token is empty or unstable: %q", first)
	}
	if first == SandboxTerminalAuditToken(7, "session-b") || first == SandboxTerminalAuditToken(8, "session-a") {
		t.Fatal("token is not tenant/session bound")
	}
	if SandboxTerminalAuditToken(0, "session-a") != "" || SandboxTerminalAuditToken(7, "") != "" {
		t.Fatal("invalid identity produced an audit token")
	}
}
