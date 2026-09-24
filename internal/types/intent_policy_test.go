package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validPolicyInput() IntentPolicyInput {
	return IntentPolicyInput{
		TenantID:       1,
		ScopeType:      PolicyScopeTool,
		ScopeRef:       "svc_wiki:wiki_delete_page",
		ConstraintText: "删除页面前必须有用户明确提到删",
		RiskTier:       RiskTierHigh,
		Mode:           VerdictModeObserve,
		Version:        1,
		CreatedBy:      "user-1",
	}
}

func TestNewIntentPolicy_Valid(t *testing.T) {
	p, err := NewIntentPolicy(validPolicyInput())
	require.NoError(t, err)
	require.NotEmpty(t, p.ID)
	require.Equal(t, PolicyScopeTool, p.ScopeType)
	require.Equal(t, RiskTierHigh, p.RiskTier)
	require.Equal(t, VerdictModeObserve, p.Mode)
	require.Equal(t, 1, p.Version)
	require.True(t, p.Enabled, "新策略默认启用")
	require.False(t, p.CreatedAt.IsZero())
	require.False(t, p.UpdatedAt.IsZero())
}

func TestNewIntentPolicy_Defaults(t *testing.T) {
	in := validPolicyInput()
	in.RiskTier = ""
	in.Mode = ""
	in.Version = 0
	p, err := NewIntentPolicy(in)
	require.NoError(t, err)
	require.Equal(t, RiskTierLow, p.RiskTier, "risk_tier 缺省 low（设计 §6.1）")
	require.Equal(t, VerdictModeObserve, p.Mode, "新策略一律 Observe 起步（设计 §6.1）")
	require.Equal(t, 1, p.Version, "version 缺省从 1 开始")
}

func TestNewIntentPolicy_RejectsUnknownEnums(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*IntentPolicyInput)
	}{
		{"scope_type", func(in *IntentPolicyInput) { in.ScopeType = "gateway" }},
		{"risk_tier", func(in *IntentPolicyInput) { in.RiskTier = "medium" }},
		{"mode", func(in *IntentPolicyInput) { in.Mode = "dry_run" }},
		{"negative version", func(in *IntentPolicyInput) { in.Version = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validPolicyInput()
			tc.mutate(&in)
			_, err := NewIntentPolicy(in)
			require.Error(t, err)
		})
	}
}

func TestNewIntentPolicy_ScopeRefRules(t *testing.T) {
	// tool/service/agent/workspace 级必须有 scope_ref，否则永远命中不了。
	for _, scope := range []string{PolicyScopeTool, PolicyScopeService, PolicyScopeAgent, PolicyScopeWorkspace} {
		in := validPolicyInput()
		in.ScopeType = scope
		in.ScopeRef = ""
		_, err := NewIntentPolicy(in)
		require.Error(t, err, "scope_type=%s 缺 scope_ref 必须拒绝", scope)
	}
	// tenant 级允许空 scope_ref（整租户生效）。
	in := validPolicyInput()
	in.ScopeType = PolicyScopeTenant
	in.ScopeRef = ""
	p, err := NewIntentPolicy(in)
	require.NoError(t, err)
	require.Equal(t, PolicyScopeTenant, p.ScopeType)
}

func TestNewIntentPolicy_RequiresConstraintText(t *testing.T) {
	in := validPolicyInput()
	in.ConstraintText = ""
	_, err := NewIntentPolicy(in)
	require.Error(t, err, "NLC 原文为空无法运营（设计 §3.2）")
}

func TestNewIntentPolicy_NullableFields(t *testing.T) {
	in := validPolicyInput()
	in.ArgPath = "$.amount"
	in.RuleExpr = "value <= 75"
	p, err := NewIntentPolicy(in)
	require.NoError(t, err)
	require.NotNil(t, p.ArgPath)
	require.Equal(t, "$.amount", *p.ArgPath)
	require.NotNil(t, p.RuleExpr)
	require.Equal(t, "value <= 75", *p.RuleExpr)

	// 缺省为 NULL：arg_path 空 = 整条调用；rule_expr 空 = 走语义层。
	p2, err := NewIntentPolicy(validPolicyInput())
	require.NoError(t, err)
	require.Nil(t, p2.ArgPath)
	require.Nil(t, p2.RuleExpr)
}
