package intentgate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// fakeGatePolicyStore 是 PolicyStore 的测试缝（spec Testing Decisions：
// gate 包测试用 fake PolicyStore）。记录每次 Resolve 的查询供断言。
type fakeGatePolicyStore struct {
	policy  *types.IntentPolicy
	err     error
	queries []ScopeQuery
}

func (f *fakeGatePolicyStore) Resolve(_ context.Context, q ScopeQuery) (*types.IntentPolicy, error) {
	f.queries = append(f.queries, q)
	return f.policy, f.err
}

func (f *fakeGatePolicyStore) InvalidateTenant(uint64) {}

// policyGateTestPolicy 构造一条租户级测试策略（version=3、observe）。
// ruleExpr 为空串表示 rule_expr 为 NULL（走语义层的策略形态）。
func policyGateTestPolicy(ruleExpr string) *types.IntentPolicy {
	p := &types.IntentPolicy{
		ID:             "pol-t23",
		TenantID:       1,
		ScopeType:      types.PolicyScopeTenant,
		ScopeRef:       "",
		ConstraintText: "单笔退款不得超过 75",
		RiskTier:       types.RiskTierLow,
		Mode:           types.VerdictModeObserve,
		Version:        3,
		Enabled:        true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if ruleExpr != "" {
		p.RuleExpr = &ruleExpr
	}
	return p
}

// TestPolicyGateNoPolicyBaselineAllow：无策略命中时走 baseline 判定，
// 无危险形态 → allow，layer=baseline，policy_id 为空（设计 §6.2 兜底判定）。
func TestPolicyGateNoPolicyBaselineAllow(t *testing.T) {
	store := &fakeGatePolicyStore{policy: nil}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "search_knowledge",
		Args:     json.RawMessage(`{"query":"活动方案"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionAllow {
		t.Fatalf("action = %q, want allow", v.Action)
	}
	if v.Layer != LayerBaseline {
		t.Fatalf("layer = %q, want baseline", v.Layer)
	}
	if v.PolicyID != "" || v.PolicyVersion != 0 {
		t.Fatalf("baseline verdict must not carry policy identity, got id=%q version=%d",
			v.PolicyID, v.PolicyVersion)
	}
}

// TestPolicyGateNoPolicyBaselineDeny：无策略命中时 baseline 仍跑 spike
// 规则（危险形态兜底扫描），命中 → deny 且 layer=baseline（不是 rule——
// rule 层专属于策略的 rule_expr 判定）。
func TestPolicyGateNoPolicyBaselineDeny(t *testing.T) {
	store := &fakeGatePolicyStore{policy: nil}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "shell_exec",
		Args:     json.RawMessage(`{"command":"rm -rf ~"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny", v.Action)
	}
	if v.Layer != LayerBaseline {
		t.Fatalf("layer = %q, want baseline", v.Layer)
	}
	if v.PolicyID != "" {
		t.Fatalf("baseline verdict policy_id = %q, want empty", v.PolicyID)
	}
}

// TestPolicyGateRuleAllow 验收 [api] 前置（unit 侧）：策略 rule_expr
// 约束满足 → allow，verdict 携带 policy_id 与 policy_version。
func TestPolicyGateRuleAllow(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy(`$.amount <= 75`)}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":60}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionAllow {
		t.Fatalf("action = %q, want allow（约束满足）", v.Action)
	}
	if v.Layer != LayerRule {
		t.Fatalf("layer = %q, want rule", v.Layer)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("verdict must carry policy identity, got id=%q version=%d", v.PolicyID, v.PolicyVersion)
	}
	if v.Mode != types.VerdictModeObserve {
		t.Fatalf("mode = %q, want observe", v.Mode)
	}
}

// TestPolicyGateRuleDeny：rule_expr 约束被违反 → deny，理由含 NLC 原文
// （enforce 接线后 agent 凭理由自我纠错，设计决策「deny 走现有工具错误路径」）。
func TestPolicyGateRuleDeny(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy(`$.amount <= 75`)}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":100}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny（约束被违反）", v.Action)
	}
	if v.Layer != LayerRule {
		t.Fatalf("layer = %q, want rule", v.Layer)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("verdict must carry policy identity, got id=%q version=%d", v.PolicyID, v.PolicyVersion)
	}
	if !strings.Contains(v.Reason, "单笔退款不得超过 75") {
		t.Fatalf("deny reason should carry NLC 原文, got %q", v.Reason)
	}
}

// TestPolicyGateArgPathExtraction：policy.arg_path 抽取参数子值后，
// rule_expr 的 value 别名指代该子值（设计 §6.1/§6.2）。
func TestPolicyGateArgPathExtraction(t *testing.T) {
	policy := policyGateTestPolicy(`value <= 75`)
	argPath := "$.amount"
	policy.ArgPath = &argPath
	store := &fakeGatePolicyStore{policy: policy}
	gate := NewPolicyGate(store)

	allow, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "refund", Args: json.RawMessage(`{"amount":60,"currency":"USD"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if allow.Action != ActionAllow {
		t.Fatalf("amount=60: action = %q, want allow", allow.Action)
	}

	deny, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "refund", Args: json.RawMessage(`{"amount":80,"currency":"USD"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if deny.Action != ActionDeny {
		t.Fatalf("amount=80: action = %q, want deny", deny.Action)
	}
}

// TestPolicyGateRuleNotApplicable：规则不适用（参数路径缺失）→
// 按设计 §8.1 应升级语义层；judge 未接入（T30），记 uncertain，绝不
// 静默判 allow/deny。
func TestPolicyGateRuleNotApplicable(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy(`$.amount <= 75`)}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"other":1}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain", v.Action)
	}
	if v.PolicyID != "pol-t23" {
		t.Fatalf("uncertain verdict still belongs to the policy, got id=%q", v.PolicyID)
	}
}

// TestPolicyGateNoRuleExprNeedsJudge：策略只有 NLC 原文、无 rule_expr →
// 需要语义层判定；judge 未接入（T30），记 uncertain 且 layer=judge。
func TestPolicyGateNoRuleExprNeedsJudge(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "wiki_delete_page",
		Args:     json.RawMessage(`{"page_id":"p_8842"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain", v.Action)
	}
	if v.Layer != LayerJudge {
		t.Fatalf("layer = %q, want judge（约束只能由语义层判定）", v.Layer)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("verdict must carry policy identity, got id=%q version=%d", v.PolicyID, v.PolicyVersion)
	}
}

// TestPolicyGateCompileErrorFailsSafe：存量脏数据（rule_expr 编译失败）
// 不得 panic、不得误判，记 uncertain。
func TestPolicyGateCompileErrorFailsSafe(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy(`exec(`)}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":60}`),
	})
	if err != nil {
		t.Fatalf("Evaluate must not surface compile error as gate failure: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain", v.Action)
	}
}

// TestPolicyGateStoreErrorPropagates：policy store DB 错误原样返回，
// 由 engine 接缝按设计 §9 fail-open（记 warn 放行）。
func TestPolicyGateStoreErrorPropagates(t *testing.T) {
	store := &fakeGatePolicyStore{err: errors.New("db is down")}
	gate := NewPolicyGate(store)

	_, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "search_knowledge",
		Args:     json.RawMessage(`{"query":"x"}`),
	})
	if err == nil {
		t.Fatal("store error must propagate (engine fail-open handles it)")
	}
}

// TestPolicyGateScopeQueryMapping：ToolCallInput 的 scope 相关字段完整
// 传入 PolicyStore 解析（tool/service/agent/workspace 各层命中靠它）。
func TestPolicyGateScopeQueryMapping(t *testing.T) {
	store := &fakeGatePolicyStore{policy: nil}
	gate := NewPolicyGate(store)

	_, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID:    42,
		SessionID:   "s-1",
		ToolName:    "wiki_get_page",
		ServiceID:   "svc-9",
		AgentID:     "agent-7",
		WorkspaceID: "ws-3",
		Args:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(store.queries) != 1 {
		t.Fatalf("Resolve calls = %d, want 1", len(store.queries))
	}
	q := store.queries[0]
	if q.TenantID != 42 || q.ToolName != "wiki_get_page" || q.ServiceID != "svc-9" ||
		q.AgentID != "agent-7" || q.WorkspaceID != "ws-3" {
		t.Fatalf("ScopeQuery mapping wrong: %+v", q)
	}
}

// TestPolicyGateEnforceModeRecordedOnly 验收 [unit] 的对照面：Gate 只产出
// verdict（含策略 mode），不依据 mode 自行拦截——observe 只记录不拦截、
// enforce 的实际阻断由 engine 接缝（T40）按 verdict.Mode 执行。
func TestPolicyGateEnforceModeRecordedOnly(t *testing.T) {
	policy := policyGateTestPolicy(`$.amount <= 75`)
	policy.Mode = types.VerdictModeEnforce
	store := &fakeGatePolicyStore{policy: policy}
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":100}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny", v.Action)
	}
	if v.Mode != types.VerdictModeEnforce {
		t.Fatalf("mode = %q, want enforce（记录判定时的策略 mode）", v.Mode)
	}
}
