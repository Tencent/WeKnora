package intentgate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// failClosePolicy 构造一条 enforce 策略（rule_expr 为空 → 必走未决路径；
// risk_tier 可调）。
func failClosePolicy(tier string) *types.IntentPolicy {
	p := policyGateTestPolicy("")
	p.RiskTier = tier
	p.Mode = types.VerdictModeEnforce
	return p
}

// TestFailOpenLowTierPassesWithWarn 验收 [unit]（issue #19）①：judge 故障
// （fake judge 返回 uncertain）+ enforce 普通策略 → 放行且留 fail_open
// 结构化告警日志。
func TestFailOpenLowTierPassesWithWarn(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	store := &fakeGatePolicyStore{policy: failClosePolicy(types.RiskTierLow)}
	judge := &fakeJudge{verdict: Verdict{Action: ActionUncertain, Layer: LayerJudge, Reason: "judge 超时（预算 3s）"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "refund", Args: json.RawMessage(`{"amount":1}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain（fail-open 放行）", v.Action)
	}
	// 阻断 = deny && Enforced()：uncertain 的 Action 不是 deny，engine 接缝
	// 不会拦截（Enforced() 为 true 只说明策略是 enforce 模式，与放行无关）。
	out := buf.String()
	if !strings.Contains(out, "intentgate.fail_open") {
		t.Fatalf("fail-open warn log missing, logs: %s", out)
	}
}

// TestFailCloseHighTierBlocks 验收 [unit]（issue #19）②：同一故障场景，
// risk_tier=high 策略 → 转 deny 且可执行（Enforced），reason 说明 fail-close。
func TestFailCloseHighTierBlocks(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	store := &fakeGatePolicyStore{policy: failClosePolicy(types.RiskTierHigh)}
	judge := &fakeJudge{verdict: Verdict{Action: ActionUncertain, Layer: LayerJudge, Reason: "judge 超时（预算 3s）"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "wiki_delete_page", Args: json.RawMessage(`{"page_id":"p1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny（high 策略 fail-close）", v.Action)
	}
	if !v.Enforced() {
		t.Fatal("fail-close deny must carry enforce mode（Mode 来自策略）")
	}
	if !strings.Contains(v.Reason, "fail-close") {
		t.Fatalf("reason 应说明 fail-close, got %q", v.Reason)
	}
	if v.PolicyID != "pol-t23" || v.PolicyVersion != 3 {
		t.Fatalf("verdict 应携带策略身份, got %q v%d", v.PolicyID, v.PolicyVersion)
	}
	if !strings.Contains(buf.String(), "intentgate.fail_close") {
		t.Fatalf("fail-close warn log missing, logs: %s", buf.String())
	}
}

// TestFailCloseEnvBlocksAll 验收 [unit]（issue #19）③：全局开关开启 →
// 普通 low 策略的未决也拦截。
func TestFailCloseEnvBlocksAll(t *testing.T) {
	t.Setenv(FailCloseEnvVar, "true")

	store := &fakeGatePolicyStore{policy: failClosePolicy(types.RiskTierLow)}
	judge := &fakeJudge{verdict: Verdict{Action: ActionUncertain, Layer: LayerJudge, Reason: "judge 输出垃圾"}}
	gate := NewPolicyGate(store, WithJudge(judge)) // 默认 ctor 读 env

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "refund", Args: json.RawMessage(`{"amount":1}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny（全局 fail-close）", v.Action)
	}
	if !strings.Contains(v.Reason, FailCloseEnvVar) {
		t.Fatalf("reason 应说明全局开关, got %q", v.Reason)
	}
}

// TestFailCloseEnvGarbageDefaultsOpen：不可解析的开关值按 false 处置
// （fail-open 是方向安全的缺省，误配不得悄悄全局拦截）。
func TestFailCloseEnvGarbageDefaultsOpen(t *testing.T) {
	t.Setenv(FailCloseEnvVar, "maybe")

	store := &fakeGatePolicyStore{policy: failClosePolicy(types.RiskTierLow)}
	judge := &fakeJudge{verdict: Verdict{Action: ActionUncertain, Layer: LayerJudge, Reason: "judge 超时"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "refund", Args: json.RawMessage(`{"amount":1}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain（垃圾开关值按 fail-open）", v.Action)
	}
}

// TestObserveNeverFailsClosed：observe 的未决即使 high 策略也不得拦截
// （observe 的定义：只记录不拦截；fail-close 只作用于 enforce）。
func TestObserveNeverFailsClosed(t *testing.T) {
	policy := failClosePolicy(types.RiskTierHigh)
	policy.Mode = types.VerdictModeObserve // observe：只记录不拦截，永不 fail-close
	store := &fakeGatePolicyStore{policy: policy}
	judge := &fakeJudge{verdict: Verdict{Action: ActionUncertain, Layer: LayerJudge, Reason: "judge 超时"}}
	gate := NewPolicyGate(store, WithJudge(judge), WithFailClose(true))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "refund", Args: json.RawMessage(`{"amount":1}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionUncertain {
		t.Fatalf("action = %q, want uncertain（observe 永不 fail-close）", v.Action)
	}
}

// TestFailCloseDegradedPath：能力档降级（judge=nil 等价的弱档形态）产生的
// 规则层未决，在 high+enforce 下同样 fail-close——语义对"语义层不可用"
// 的所有形态一致。
func TestFailCloseDegradedPath(t *testing.T) {
	store := &fakeGatePolicyStore{policy: failClosePolicy(types.RiskTierHigh)}
	// 无 rule_expr + judge=nil → evaluatePolicyRule 出 uncertain → 无升级。
	gate := NewPolicyGate(store)

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "wiki_delete_page", Args: json.RawMessage(`{"page_id":"p1"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionDeny {
		t.Fatalf("action = %q, want deny（降级未决 + high + enforce → fail-close）", v.Action)
	}
	if !strings.Contains(v.Reason, "fail-close") {
		t.Fatalf("reason 应说明 fail-close, got %q", v.Reason)
	}
}

// TestRequireApprovalSentinel：哨兵值确定产出 require_approval（#31 的
// 确定性验收触发器）；high 策略也不被 judge 复核绕过。
func TestRequireApprovalSentinel(t *testing.T) {
	sentinel := RuleExprRequireApprovalSentinel
	policy := policyGateTestPolicy("")
	policy.RuleExpr = &sentinel
	policy.Mode = types.VerdictModeEnforce
	policy.RiskTier = types.RiskTierHigh // 连 high 也不得绕过
	store := &fakeGatePolicyStore{policy: policy}
	judge := &fakeJudge{verdict: Verdict{Action: ActionAllow, Layer: LayerJudge, Reason: "judge 说放行"}}
	gate := NewPolicyGate(store, WithJudge(judge))

	v, err := gate.Evaluate(context.Background(), ToolCallInput{
		TenantID: 1, ToolName: "mcp__svc__danger", Args: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Action != ActionRequireApproval {
		t.Fatalf("action = %q, want require_approval（哨兵）", v.Action)
	}
	if v.Layer != LayerRule {
		t.Fatalf("layer = %q, want rule", v.Layer)
	}
	if !v.Enforced() {
		t.Fatal("enforce 策略的哨兵 verdict 必须 Enforced（seam 才会挂审批标记）")
	}
	if !strings.Contains(v.Reason, "require_approval") {
		t.Fatalf("reason 应说明哨兵, got %q", v.Reason)
	}
	// judge 不得被调用（哨兵是终态）。
	if judge.calls != 0 {
		t.Fatalf("哨兵不得升级 judge, calls=%d", judge.calls)
	}
}
