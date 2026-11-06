package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolApprovalDecisionCarriesHostScope(t *testing.T) {
	d, err := toolApprovalDecision(resolveToolApprovalBody{Decision: "approve", Scope: "session", Access: "read"})
	require.NoError(t, err)
	require.True(t, d.Approved)
	require.Equal(t, "session", d.Scope)
	require.Equal(t, "read", d.Access)
}

func TestToolApprovalDecisionRejectsUnknownScopeOrAccess(t *testing.T) {
	_, err := toolApprovalDecision(resolveToolApprovalBody{Decision: "approve", Scope: "forever"})
	require.Error(t, err)
	_, err = toolApprovalDecision(resolveToolApprovalBody{Decision: "approve", Access: "admin"})
	require.Error(t, err)
}

func TestToolApprovalDecisionKeepsMCPBehaviour(t *testing.T) {
	d, err := toolApprovalDecision(resolveToolApprovalBody{
		Decision: "approve", ModifiedArgs: json.RawMessage(`{"a":1}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"a":1}`, string(d.ModifiedArgs))

	_, err = toolApprovalDecision(resolveToolApprovalBody{Decision: "approve", ModifiedArgs: json.RawMessage(`null`)})
	require.NoError(t, err)
	_, err = toolApprovalDecision(resolveToolApprovalBody{Decision: "approve", ModifiedArgs: json.RawMessage(`[1]`)})
	require.Error(t, err)

	d, err = toolApprovalDecision(resolveToolApprovalBody{Decision: "reject", Reason: "no"})
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.Equal(t, "no", d.Reason)

	_, err = toolApprovalDecision(resolveToolApprovalBody{Decision: "maybe"})
	require.Error(t, err)
}
