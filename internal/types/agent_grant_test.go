package types

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAgentGrantActiveAt(t *testing.T) {
	now := time.Now()
	session := &AgentGrant{Scope: AgentGrantScopeSession, CreatedAt: now.Add(-365 * 24 * time.Hour)}
	assert.True(t, session.ActiveAt(now, ""), "a session grant lasts as long as its conversation")
	assert.False(t, session.ActiveAt(now, "other"), "a grant applies only under its binding")

	automation := &AgentGrant{
		Scope: AgentGrantScopeAutomation, BindingHash: "b", CreatedAt: now.Add(-100 * 24 * time.Hour),
	}
	assert.False(t, automation.ActiveAt(now, "b"), "an automation grant unused for 90 days lapses")
	used := now.Add(-time.Hour)
	automation.LastUsedAt = &used
	assert.True(t, automation.ActiveAt(now, "b"))

	expired := now.Add(-time.Minute)
	assert.False(t, (&AgentGrant{Scope: AgentGrantScopeSession, ExpiresAt: &expired}).ActiveAt(now, ""))
	assert.False(t, (&AgentGrant{Scope: AgentGrantScopeSession, RevokedAt: &expired}).ActiveAt(now, ""))
	assert.False(t, (*AgentGrant)(nil).ActiveAt(now, ""))
}

func TestAgentGrantLapseReasons(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	assert.Equal(t, AgentGrantLapseRevoked, (&AgentGrant{RevokedAt: &past}).LapseAt(now, ""))
	assert.Equal(t, AgentGrantLapseExpired, (&AgentGrant{ExpiresAt: &past}).LapseAt(now, ""))
	assert.Equal(t, AgentGrantLapseChanged, (&AgentGrant{BindingHash: "a"}).LapseAt(now, "b"))
	idle := &AgentGrant{Scope: AgentGrantScopeAutomation, CreatedAt: now.Add(-AgentGrantIdleExpiry)}
	assert.Equal(t, AgentGrantLapseIdle, idle.LapseAt(now, ""))
	assert.Empty(t, (&AgentGrant{Scope: AgentGrantScopeSession}).LapseAt(now, ""))
}

func TestAutomationBindingHash(t *testing.T) {
	base := AutomationBinding{
		AgentID: "a", Tools: []string{"x", "y"}, MCPServiceIDs: []string{"m1", "m2"},
		SystemPrompt: "p", OwnerRole: TenantRoleContributor, Ceiling: AssistantAccessAutoWrite,
	}
	reordered := base
	reordered.Tools = []string{"y", "x"}
	reordered.MCPServiceIDs = []string{"m2", "m1"}
	assert.Equal(t, base.Hash(), reordered.Hash(), "order does not matter")
	assert.Len(t, base.Hash(), 64, "fits binding_hash")

	for name, change := range map[string]func(*AutomationBinding){
		"tools":   func(b *AutomationBinding) { b.Tools = []string{"x"} },
		"mcp":     func(b *AutomationBinding) { b.MCPServiceIDs = []string{"m1", "m3"} },
		"prompt":  func(b *AutomationBinding) { b.SystemPrompt = "q" },
		"role":    func(b *AutomationBinding) { b.OwnerRole = TenantRoleViewer },
		"ceiling": func(b *AutomationBinding) { b.Ceiling = AssistantAccessReadOnly },
	} {
		changed := base
		changed.Tools = slices.Clone(base.Tools)
		change(&changed)
		assert.NotEqual(t, base.Hash(), changed.Hash(), name)
	}
	assert.Equal(t, []string{"x", "y"}, base.Tools, "hashing leaves the binding unchanged")
}
