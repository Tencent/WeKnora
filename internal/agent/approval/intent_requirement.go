// IntentGate 的 require_approval 到审批通道的 ctx 载体（T41，issue #18）。
//
// enforce 模式下 IntentGate 判出 require_approval 时，engine 接缝把"本次
// 调用须经人工审批 + 策略理由"写进执行 ctx；MCP 工具执行时读到该标记
// 即复用现有审批通道（RequestAndWait：阻塞等待 + 跨实例广播 + 改参数
// 放行），与按工具配置的审批策略走完全同一条路径，不新增交互形态。
// 设计 §7 注记：人工审批通道只覆盖 MCP 工具，内置工具的 require_approval
// 需要新的暂停-恢复点，本包不涉及。
package approval

import "context"

// intentRequirementContextKey 是 IntentGate 强制审批标记的 ctx 键。
type intentRequirementContextKey struct{}

// WithIntentRequirement 把 IntentGate 的强制审批要求写进 ctx：reason
// 为策略判定的理由（含 NLC 原文），审批卡片把它展示给操作人。engine
// 接缝在 verdict=require_approval 且 enforce 且目标是 MCP 工具时调用。
func WithIntentRequirement(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, intentRequirementContextKey{}, reason)
}

// IntentRequirementFromContext 读取强制审批要求；未标记返回 ("", false)。
func IntentRequirementFromContext(ctx context.Context) (string, bool) {
	reason, ok := ctx.Value(intentRequirementContextKey{}).(string)
	return reason, ok
}
