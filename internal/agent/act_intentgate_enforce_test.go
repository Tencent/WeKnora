package agent

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/types"
)

// TestIntentGateEnforceDenyBlocksTool 验收 [unit]（issue #17）：enforce
// 模式的 deny → 工具未被执行（执行次数断言），toolCall.Result.Success=false
// 且错误文本含命中理由（agent 自我纠错的依据）。
func TestIntentGateEnforceDenyBlocksTool(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{verdict: intentgate.Verdict{
		Action:        intentgate.ActionDeny,
		PolicyID:      "pol-enforce-1",
		PolicyVersion: 2,
		Mode:          types.VerdictModeEnforce,
		Layer:         intentgate.LayerRule,
		Reason:        "违反策略约束「单笔退款不得超过 75」（rule_expr: 1 > 2）",
	}}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	if *executed != 0 {
		t.Fatalf("enforce deny must block execution, executed=%d", *executed)
	}
	if toolCall.Result == nil {
		t.Fatal("enforce deny must produce an error tool result")
	}
	if toolCall.Result.Success {
		t.Fatalf("enforce deny must fail the tool call, got %+v", toolCall.Result)
	}
	// 错误文本必须含策略 NLC 原文——agent 下一轮凭它解释并自我纠错。
	if !strings.Contains(toolCall.Result.Error, "单笔退款不得超过 75") {
		t.Fatalf("error must carry the NLC reason, got %q", toolCall.Result.Error)
	}
	if !strings.Contains(toolCall.Result.Error, "IntentGate") {
		t.Fatalf("error should identify the intent gate, got %q", toolCall.Result.Error)
	}
	if gate.callCount() != 1 {
		t.Fatalf("gate must be consulted exactly once, got %d", gate.callCount())
	}
}

// TestIntentGateObserveDenyUnchanged 验收 [unit] 的反向面：同样 deny
// verdict、mode 缺省（=observe）→ 工具照常执行、结果成功。防过度拦截。
func TestIntentGateObserveDenyUnchanged(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{verdict: intentgate.Verdict{
		Action:   intentgate.ActionDeny,
		PolicyID: "pol-observe-1",
		Reason:   "违反策略约束「xxx」",
		Layer:    intentgate.LayerRule,
		// Mode 故意留空（无策略命中/基线判定的形态）：绝不得当 enforce。
	}}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("observe/empty-mode deny must not block, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("observe/empty-mode deny must keep the tool call successful, got %+v", toolCall.Result)
	}
}

// TestIntentGateEnforceUncertainPasses：enforce 策略下 uncertain 在 T40
// 范围不阻断（放行 + 记录），处置归 T42。防止把「未决」误升级为「拦截」。
func TestIntentGateEnforceUncertainPasses(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{verdict: intentgate.Verdict{
		Action: intentgate.ActionUncertain,
		Mode:   types.VerdictModeEnforce,
		Layer:  intentgate.LayerJudge,
		Reason: "judge 超时",
	}}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("enforce uncertain must pass in T40 scope, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("enforce uncertain must keep the tool call successful, got %+v", toolCall.Result)
	}
}

// TestIntentGateEnforceAllowPasses：enforce 策略判 allow → 执行。拦截面
// 的最窄化校验：只有 deny+enforce 才阻断。
func TestIntentGateEnforceAllowPasses(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{verdict: intentgate.Verdict{
		Action: intentgate.ActionAllow,
		Mode:   types.VerdictModeEnforce,
		Layer:  intentgate.LayerRule,
		Reason: "约束满足",
	}}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("enforce allow must execute, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("enforce allow must succeed, got %+v", toolCall.Result)
	}
}

// TestDeniedErrorMessage：错误文本契约（agent 可见性 + e2e 断言锚点）。
func TestDeniedErrorMessage(t *testing.T) {
	err := &intentgate.DeniedError{Verdict: intentgate.Verdict{
		Action: intentgate.ActionDeny,
		Reason: "违反策略约束「禁止删除」",
		Mode:   types.VerdictModeEnforce,
	}}
	msg := err.Error()
	if !strings.Contains(msg, "被意图策略拒绝") {
		t.Fatalf("message = %q", msg)
	}
	if !strings.Contains(msg, "禁止删除") {
		t.Fatalf("message must carry reason, got %q", msg)
	}
}
