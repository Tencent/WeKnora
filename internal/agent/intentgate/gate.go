// Package intentgate 实现 IntentGate（语义门禁层）的包骨架。
//
// IntentGate 是 Agent 执行循环内部的策略执行点（PEP），在工具调用前
// 判定"该不该做"（意图对齐 + 自然语言约束合规），产出 Verdict。
// 术语见 CONTEXT.md；详细设计见 docs/plans/2026-09-21-intent-gate-design.md。
//
// 本文件只定义契约与默认 NoopGate 实现；规则引擎（RuleEngine）、
// 语义层 judge（Judge）、策略存储（PolicyStore）在后续 ticket 落地。
package intentgate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// Action 是 Verdict 的四种判定取值。序列化为字符串，与 intent_verdict
// 表的 verdict 枚举值一致，保证日志可读、落库可对账。
type Action string

const (
	// ActionAllow 放行。
	ActionAllow Action = "allow"
	// ActionDeny 拒绝（observe 模式下只记录不拦截）。
	ActionDeny Action = "deny"
	// ActionRequireApproval 转人工审批（MCP 工具复用现有审批门通道）。
	ActionRequireApproval Action = "require_approval"
	// ActionUncertain 无法判定（judge 解析失败等）；处置见设计 §8.2。
	ActionUncertain Action = "uncertain"
)

// UnmarshalJSON 拒绝未知取值，防止脏数据静默进入 verdict 链路。
func (a *Action) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("intentgate.Action: %w", err)
	}
	switch Action(s) {
	case ActionAllow, ActionDeny, ActionRequireApproval, ActionUncertain:
		*a = Action(s)
		return nil
	default:
		return fmt.Errorf("intentgate.Action: unknown value %q", s)
	}
}

// Layer 标记 Verdict 出自哪一层判定。
type Layer string

const (
	// LayerRule ① 确定性规则层。
	LayerRule Layer = "rule"
	// LayerJudge ② 语义层（LLM judge）。
	LayerJudge Layer = "judge"
	// LayerBaseline 无策略命中时的基线扫描（policy_id 为空）。
	LayerBaseline Layer = "baseline"
)

// Verdict 是 IntentGate 对一次工具调用的判定结果。
// 与审批门的 Decision（人工审批结果）不是同一物，命名上严格区分。
type Verdict struct {
	Action   Action `json:"action"`
	PolicyID string `json:"policy_id,omitempty"`
	// PolicyVersion 判定依据的策略版本（设计 §6.2：verdict 表记着每次判定
	// 用的是哪个版本，dev/prod 行为不一致时可对账）。无策略命中时为 0。
	PolicyVersion int `json:"policy_version,omitempty"`
	// Mode 判定时的策略 mode（observe/enforce，取值与 types.VerdictMode*
	// 一致），落库为 mode_at_decision。Gate 只记录不据此拦截——observe
	// 的放行与 enforce 的阻断都由 engine 接缝执行（T40）。无策略命中时为空。
	Mode   string `json:"mode,omitempty"`
	Reason string `json:"reason,omitempty"`
	Layer  Layer  `json:"layer,omitempty"`
	// JudgeTokens 是本次 judge 调用消耗的 token 数（设计 §6.2 成本观测，
	// 落 intent_verdicts.judge_tokens）。规则层/baseline 判定为 0；命中
	// 判定缓存时为原始那次调用的消耗。T32 起由 LLMJudge 填充。
	JudgeTokens int `json:"judge_tokens,omitempty"`
}

// Enforced 报告该 verdict 是否应按 enforce 语义动作：判定时的策略 mode
// 为 enforce（types.VerdictModeEnforce）。只有 Action==Deny 且 Enforced()
// 的 verdict 才在 engine 接缝转成 DeniedError 阻断工具调用（T40，设计 §9）；
// observe 的 deny 只记录不拦截，require_approval/uncertain 的 enforce
// 处置归 T41/T42。
func (v Verdict) Enforced() bool {
	return v.Mode == types.VerdictModeEnforce
}

// DeniedError 是 enforce 模式下 deny verdict 的错误形态（设计 §7「deny
// 走现有 err 路径」）：toolCall.Result.Success=false、Error 携带策略 NLC
// 理由，agent 下一轮凭理由在对话中解释并自我纠错。区别于审批门的
// 拒绝（人工 Decision），这是策略自动判定。
type DeniedError struct {
	Verdict Verdict
}

// Error 实现 error 接口。文本必须包含 verdict.Reason（含策略 NLC 原文），
// 这是 agent 自我纠错的全部依据；前缀固定便于日志检索与 e2e 断言。
func (e *DeniedError) Error() string {
	v := e.Verdict
	if v.Reason == "" {
		v.Reason = "未说明原因"
	}
	return fmt.Sprintf("[IntentGate] 工具调用被意图策略拒绝：%s", v.Reason)
}

// ToolCallInput 是一次工具调用的判定输入。意图基准是原始 user prompt
// 与会话历史，不是当前轮的模型输出（被审对象不能自证，设计 §8.2）。
type ToolCallInput struct {
	TenantID  uint64 `json:"tenant_id"`
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	ServiceID string `json:"service_id,omitempty"` // MCP 工具才有
	// AgentID / WorkspaceID 参与 scope 解析（设计 §8.3 的 agent/workspace
	// 层级）；engine 接缝尚未填充时为空，对应层级不参与解析。
	AgentID     string          `json:"agent_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	// UserPrompt 原始用户 prompt（本轮会话第一条 user message）。
	UserPrompt string `json:"user_prompt,omitempty"`
	// History engine 现有 messages 的窗口（默认最近 8 条）。
	History   []types.Message `json:"history,omitempty"`
	Principal types.Principal `json:"principal,omitempty"`
}

// Gate 是 IntentGate 对外契约：engine 在工具执行点（act.go）前调用
// Evaluate 取得 Verdict，再按策略 mode（observe/enforce）决定动作。
// engine 测试以 fake Gate 为测试缝。
type Gate interface {
	Evaluate(ctx context.Context, in ToolCallInput) (Verdict, error)
}

// NoopGate 是默认实现：恒返回 allow。用于 IntentGate 未启用时保持
// 现网行为零变化，也是各层失败时 fail-open 语义的兜底形态。
type NoopGate struct{}

// NewNoopGate 返回恒放行的 Gate。
func NewNoopGate() *NoopGate {
	return &NoopGate{}
}

// Evaluate 恒返回 allow verdict。
func (NoopGate) Evaluate(_ context.Context, _ ToolCallInput) (Verdict, error) {
	return Verdict{Action: ActionAllow}, nil
}
