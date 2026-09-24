// Judge 模型能力档（issue #15 / T31，设计 §12「用小模型（租户可配）」）。
//
// judge 对模型的要求有两条硬杠：① 可靠的指令遵循 + 结构化 JSON 输出
// （解析失败=uncertain，弱模型会把一切判定拖成 uncertain，门禁形同虚设）；
// ② 上下文窗口装得下 judge 输入预算（原始 prompt 2k + 历史 8×1k +
// 参数 4k + system ≈ 8k tokens）。
//
// 判定优先级（显式配置 > 结构性启发式）：
//  1. 模型 ExtraConfig["intentgate_judge_capable"] = "true"/"false"
//     （租户管理员显式声明，设计"租户可配"的落地口）；
//  2. 上下文窗口已知且 < judgeMinContextTokens → 弱档（装不下输入）；
//  3. 本地模型（Source=local，Ollama 等）默认弱档——小参数本地模型的
//     指令遵循不足以承担安全判定，除非显式打开；
//  4. 其余（远程商业 API 模型）→ 强档。
//
// 弱档的处置不在本文件：LLMJudge.Enabled 报 false，PolicyGate 降级只跑
// 规则层并记 layer=rule（issue #15 验收语义）。
package intentgate

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// JudgeCapableExtraConfigKey 是模型 ExtraConfig 里 judge 能力档的显式
// 开关键。取值 "true"/"on"/"1" = 强档，"false"/"off"/"0" = 弱档；
// 缺省或未识别 → 回落到结构性启发式。
const JudgeCapableExtraConfigKey = "intentgate_judge_capable"

// judgeMinContextTokens 是 judge 输入预算下限：原始 prompt（2k）+ 历史
// 窗口（8×1k）+ 参数（4k）+ system prompt 的余量。窗口已知且低于此值
// 的模型装不下 judge 输入，判弱档。
const judgeMinContextTokens = 8192

// JudgeCapable 报告模型是否达到 judge 能力档。nil 模型按弱档处置
// （宁可降级，不让未知模型碰安全判定）。
func JudgeCapable(m *types.Model) bool {
	if m == nil {
		return false
	}
	if v, ok := m.Parameters.ExtraConfig[JudgeCapableExtraConfigKey]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "on", "1":
			return true
		case "false", "off", "0":
			return false
		}
		// 未识别取值不当作显式声明，继续走启发式。
	}
	if cw := m.Parameters.ContextWindow; cw > 0 && cw < judgeMinContextTokens {
		return false
	}
	if m.Source == types.ModelSourceLocal {
		return false
	}
	return true
}
