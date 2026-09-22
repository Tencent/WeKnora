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
// 按设计 §8.1 应升级语义层；judge 未配置（nil）时记 uncertain，绝不
// 静默判 allow/deny。judge 接入后的升级行为见
// TestPolicyGateRuleNotApplicableWithJudge。
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
// 需要语义层判定；judge 未配置（nil）时记 uncertain 且 layer=judge。
// judge 接入后的真升级见 TestPolicyGateNoRuleExprWithJudge。
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

// fakeJudge 是 Judge 的测试缝：记录输入、按预设返回 verdict/错误。
type fakeJudge struct {
	verdict Verdict
	err     error
	calls   int
	inputs  []JudgeInput
}

func (f *fakeJudge) Judge(_ context.Context, in JudgeInput) (Verdict, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	return f.verdict, f.err
}

// TestPolicyGateNoRuleExprWithJudge：无 rule_expr 的策略在 judge 接入后
// 真升级语义层——fake judge 判 allow → verdict=allow、layer=judge，且
// 策略身份与意图基准完整传递（设计 §8.1 漏斗接通）。
func TestPolicyGateNoRuleExprWithJudge(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")}
	judge := &fakeJudge{verdict: Verdict{Action: ActionAllow, Layer: LayerJudge, Reason: "用户明确要求删除"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	history := []types.Message{{Role: "user", Content: "把过时页面清掉"}}
	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID:   1,
		ToolName:   "wiki_delete_page",
		Args:       json.RawMessage(`{"page_id":"p_8842"}`),
		UserPrompt: "把过时页面清掉",
		History:    history,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("judge calls = %d, want 1", judge.calls)
	}
	if v.Action != ActionAllow {
		t.Fatalf("action = %q, want allow（judge 放行）", v.Action)
	}
	if v.Layer != LayerJudge {
		t.Fatalf("layer = %q, want judge", v.Layer)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("judge verdict must carry policy identity, got id=%q version=%d", v.PolicyID, v.PolicyVersion)
	}
	in := judge.inputs[0]
	if in.ConstraintText != "单笔退款不得超过 75" {
		t.Fatalf("judge input constraint = %q, want NLC 原文", in.ConstraintText)
	}
	if in.UserPrompt != "把过时页面清掉" || len(in.History) != 1 {
		t.Fatalf("judge input 意图基准丢失: %+v", in)
	}
	if string(in.Args) != `{"page_id":"p_8842"}` {
		t.Fatalf("judge input args = %s", in.Args)
	}
}

// TestPolicyGateRuleNotApplicableWithJudge：rule_expr 不适用（参数路径
// 缺失）→ 升级 judge；judge 判 deny → deny 且 layer=judge。
func TestPolicyGateRuleNotApplicableWithJudge(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy(`$.amount <= 75`)}
	judge := &fakeJudge{verdict: Verdict{Action: ActionDeny, Layer: LayerJudge, Reason: "用户未提及该笔退款"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"other":1}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("judge calls = %d, want 1", judge.calls)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny（judge 拒绝）", v.Action)
	}
	if v.Layer != LayerJudge {
		t.Fatalf("layer = %q, want judge", v.Layer)
	}
	if v.PolicyID != "pol-t23" {
		t.Fatalf("judge verdict must carry policy identity, got %q", v.PolicyID)
	}
}

// TestPolicyGateHighRiskForcesJudge：risk_tier=high 的策略即使规则层判
// allow 也强制语义层复核（设计 §8.1：高危操作要语义兜底）。
func TestPolicyGateHighRiskForcesJudge(t *testing.T) {
	policy := policyGateTestPolicy(`$.amount <= 75`)
	policy.RiskTier = types.RiskTierHigh
	store := &fakeGatePolicyStore{policy: policy}
	judge := &fakeJudge{verdict: Verdict{Action: ActionDeny, Layer: LayerJudge, Reason: "金额异常"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":60}`), // 规则层满足（<=75）
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("high 策略必须强制 judge 复核, calls = %d", judge.calls)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny（judge 复核拒绝）", v.Action)
	}
	if v.Layer != LayerJudge {
		t.Fatalf("layer = %q, want judge", v.Layer)
	}
}

// TestPolicyGateHighRiskRuleDenyStaysRule：规则层已决的 deny 不再升级
// judge——复核确定性结论只会引入不确定性和成本。
func TestPolicyGateHighRiskRuleDenyStaysRule(t *testing.T) {
	policy := policyGateTestPolicy(`$.amount <= 75`)
	policy.RiskTier = types.RiskTierHigh
	store := &fakeGatePolicyStore{policy: policy}
	judge := &fakeJudge{verdict: Verdict{Action: ActionAllow, Layer: LayerJudge}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":100}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 0 {
		t.Fatalf("规则层 deny 不得升级 judge, calls = %d", judge.calls)
	}
	if v.Action != ActionDeny || v.Layer != LayerRule {
		t.Fatalf("verdict = %q/%q, want deny/rule", v.Action, v.Layer)
	}
}

// TestPolicyGateLowRiskRuleAllowSkipsJudge：low 策略规则层判 allow 即
// 收工——语义层只在"未决"或"高危"时介入（成本控制，设计 §12）。
func TestPolicyGateLowRiskRuleAllowSkipsJudge(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy(`$.amount <= 75`)}
	judge := &fakeJudge{verdict: Verdict{Action: ActionDeny, Layer: LayerJudge}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "refund",
		Args:     json.RawMessage(`{"amount":60}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 0 {
		t.Fatalf("low 策略 allow 不应进 judge, calls = %d", judge.calls)
	}
	if v.Action != ActionAllow || v.Layer != LayerRule {
		t.Fatalf("verdict = %q/%q, want allow/rule", v.Action, v.Layer)
	}
}

// TestPolicyGateJudgeErrorFailsOpen：judge 调用失败按设计 §9 fail-open
// 记 uncertain（不阻断、不上抛成 gate 错误），verdict 仍归属策略。
func TestPolicyGateJudgeErrorFailsOpen(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")}
	judge := &fakeJudge{err: errors.New("judge exploded")}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "wiki_delete_page",
		Args:     json.RawMessage(`{"page_id":"p_1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain（judge 失败 fail-open）", v.Action)
	}
	if v.Layer != LayerJudge {
		t.Fatalf("layer = %q, want judge", v.Layer)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("uncertain verdict still belongs to the policy, got id=%q version=%d", v.PolicyID, v.PolicyVersion)
	}
	if !strings.Contains(v.Reason, "fail-open") {
		t.Fatalf("reason should record fail-open escalation, got %q", v.Reason)
	}
}

// TestPolicyGateJudgeReasonPreservesEscalation：升级路径的 reason 保留
// 规则层原始原因，事后可对账"当初为什么进 judge"。
func TestPolicyGateJudgeReasonPreservesEscalation(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")}
	judge := &fakeJudge{verdict: Verdict{Action: ActionAllow, Layer: LayerJudge, Reason: "意图对齐"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "wiki_delete_page",
		Args:     json.RawMessage(`{"page_id":"p_1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(v.Reason, "需语义层判定") || !strings.Contains(v.Reason, "judge: 意图对齐") {
		t.Fatalf("reason 应组合理由链, got %q", v.Reason)
	}
}

// fakeTieredJudge 实现 tenantCapabilityJudge：带能力档开关的 fake judge，
// 用于 T31 降级路径测试。enabled=false 时 PolicyGate 不得调用 Judge。
type fakeTieredJudge struct {
	fakeJudge
	enabled bool
}

func (f *fakeTieredJudge) Enabled(context.Context, uint64) bool { return f.enabled }

// TestPolicyGateWeakTierDegradesToRule 验收 [unit]：弱档模型 → Evaluate
// 不发起 judge 调用，verdict 记 rule 层（issue #15 验收语义）。
func TestPolicyGateWeakTierDegradesToRule(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")} // 无 rule_expr
	judge := &fakeTieredJudge{
		fakeJudge: fakeJudge{verdict: Verdict{Action: ActionDeny, Layer: LayerJudge, Reason: "不应被用到"}},
		enabled:   false,
	}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "wiki_delete_page",
		Args:     json.RawMessage(`{"page_id":"p_1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 0 {
		t.Fatalf("弱档模型不得发起 judge 调用, calls = %d", judge.calls)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain（规则层未决，降级后维持原判）", v.Action)
	}
	if v.Layer != LayerRule {
		t.Fatalf("layer = %q, want rule（降级只跑规则层并记 layer=rule）", v.Layer)
	}
	if !strings.Contains(v.Reason, "降级") {
		t.Fatalf("reason 应记录降级事件, got %q", v.Reason)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("degraded verdict still belongs to the policy, got id=%q version=%d", v.PolicyID, v.PolicyVersion)
	}
}

// TestPolicyGateCapableTierEscalates：对照面——强档模型正常升级 judge，
// 行为与 T30 一致。
func TestPolicyGateCapableTierEscalates(t *testing.T) {
	store := &fakeGatePolicyStore{policy: policyGateTestPolicy("")}
	judge := &fakeTieredJudge{
		fakeJudge: fakeJudge{verdict: Verdict{Action: ActionAllow, Layer: LayerJudge, Reason: "意图对齐"}},
		enabled:   true,
	}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1,
		ToolName: "wiki_delete_page",
		Args:     json.RawMessage(`{"page_id":"p_1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("强档模型应正常升级 judge, calls = %d", judge.calls)
	}
	if v.Action != ActionAllow || v.Layer != LayerJudge {
		t.Fatalf("verdict = %q/%q, want allow/judge", v.Action, v.Layer)
	}
}
