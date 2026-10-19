package types

import (
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
