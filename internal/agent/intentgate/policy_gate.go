// PolicyGate：PolicyStore 驱动的 Gate 实现（issue #13 / T23，设计 §7）。
//
// 判定输入从硬编码规则切换为读策略库：
//  1. PolicyStore.Resolve 解析本次调用命中的最具体策略（scope 顺序与
//     per-tenant 缓存见 policy_store.go，设计 §8.3）；
//  2. 命中策略且有 rule_expr → ① 确定性规则层判定（编译失败/不适用/
//     超时记 uncertain 升级语义层，绝不静默放行或误拦）；
//  3. 命中策略但无 rule_expr → 约束只能由语义层判定，升级 judge
//     （T30 接入；judge 未配置时记 uncertain、layer=judge）；
//  4. risk_tier=high 的策略即使规则层判 allow 也强制进语义层复核
//     （设计 §8.1：高危操作要语义兜底）；
//  5. 无策略命中 → baseline 判定：复用 spike 规则做危险形态兜底扫描，
//     layer=baseline、policy_id 为空（设计 §6.2 兜底判定）。
//
// Gate 只产出 Verdict，不依据策略 mode 自行拦截：observe 只记录不拦截、
// enforce 的实际阻断由 engine 接缝按 Verdict.Mode 执行（T40）。policy
// store DB 错误原样返回，engine 接缝按设计 §9 fail-open。
package intentgate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// PolicyGate 是设计 §7 Gate 结构的当前形态：store（策略读取）+ baseline
// 兜底扫描 + judge（语义层，T30 起接入，可为 nil）。
//
// 并发安全：PolicyGate 本身无状态（策略解析状态都在 PolicyStore 内，
// judge 实现亦无状态），单实例可安全地被全部 engine 共享。
type PolicyGate struct {
	store    PolicyStore
	baseline *RuleEngine
	judge    Judge
	// failClose 是 T42 全局失败语义开关（WEKNORA_INTENTGATE_FAIL_CLOSE）：
	// true 时 enforce 策略下一切"未决"（规则不适用/judge 故障/降级）一律
	// 拦截；false（默认）时仅 risk_tier=high 策略故障拦截，其余放行并告警。
	failClose bool
}

// PolicyGateOption 定制 PolicyGate（注入 judge 等）。
type PolicyGateOption func(*PolicyGate)

// WithJudge 装配语义层 judge（T30）。nil 表示语义层未配置：规则层
// 未决的判定保持 uncertain（与 T23 行为一致）。
func WithJudge(j Judge) PolicyGateOption {
	return func(g *PolicyGate) { g.judge = j }
}

// FailCloseEnvVar 是全局失败语义开关的环境变量名（设计 §9 决策 3）。
// 任意可 strconv.ParseBool 的真值（true/1/on…）开启：enforce 策略下
// 一切未决判定 fail-close。
const FailCloseEnvVar = "WEKNORA_INTENTGATE_FAIL_CLOSE"

// WithFailClose 覆盖 fail-close 全局开关（默认读 FailCloseEnvVar，
// 未设/不可解析为 false）。测试用它避免污染环境变量。
func WithFailClose(enabled bool) PolicyGateOption {
	return func(g *PolicyGate) { g.failClose = enabled }
}

// failCloseFromEnv 解析全局开关环境变量；未设置或不可解析一律 false
//（fail-open 是默认值，方向安全的缺省：误配不得悄悄变成全局拦截）。
func failCloseFromEnv() bool {
	raw := strings.TrimSpace(os.Getenv(FailCloseEnvVar))
	if raw == "" {
		return false
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return on
}

// NewPolicyGate 创建 PolicyStore 驱动的 Gate。store 必须是与策略 CRUD
// handler 共享的同一实例——策略变更的 InvalidateTenant 才能生效
// （设计 §8.3：策略变更按 tenant 失效解析缓存）。
func NewPolicyGate(store PolicyStore, opts ...PolicyGateOption) *PolicyGate {
	g := &PolicyGate{store: store, baseline: NewSpikeRuleEngine(), failClose: failCloseFromEnv()}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Evaluate 实现 Gate 接口。
func (g *PolicyGate) Evaluate(ctx context.Context, in ToolCallInput) (Verdict, error) {
	policy, err := g.store.Resolve(ctx, ScopeQuery{
		TenantID:    in.TenantID,
		ToolName:    in.ToolName,
		ServiceID:   in.ServiceID,
		AgentID:     in.AgentID,
		WorkspaceID: in.WorkspaceID,
	})
	if err != nil {
		// 设计 §9：policy store DB 错误 → observe 放行。Gate 不自行决定
		// 失败语义，原样上抛，由 engine 接缝 fail-open。
		return Verdict{}, fmt.Errorf("intentgate: resolve policy: %w", err)
	}
	if policy == nil {
		return g.baselineVerdict(in), nil
	}
	v := evaluatePolicyRule(policy, in)
	switch {
	case v.Action == ActionDeny:
		// 规则层已决：约束被违反，deny 直接出，不再升级语义层
		// （复核一个确定性结论只会引入不确定性和成本）。
	case v.Action == ActionRequireApproval:
		// 哨兵已决：require_approval 是管理员的显式指令，与 deny 同级
		// 的终态——high 策略的 judge 复核不得绕过人工确认（复核模型
		// 说 allow 也不能取消管理员要求的审批）。
	case policy.RiskTier == types.RiskTierHigh:
		// 设计 §8.1：high 策略即使规则层判 allow 也强制进语义层复核——
		// 高危操作要语义兜底，不允许"规则没写全就当安全"。
		v = g.judgeEscalate(ctx, policy, in, v)
	case v.Action == ActionUncertain:
		// 规则层未决（无 rule_expr / 编译失败 / 不适用 / 超时）→ 语义层。
		v = g.judgeEscalate(ctx, policy, in, v)
	}
	// T42 失败语义（设计 §9 决策 3）：enforce 下的"未决"（规则不适用/
	// 编译失败/judge 故障/能力档降级）默认 fail-open 放行并告警；
	// risk_tier=high 策略或全局开关时 fail-close 拦截。observe 永不拦截。
	v = g.applyFailSemantics(ctx, policy, in, v)
	v.PolicyID = policy.ID
	v.PolicyVersion = policy.Version
	v.Mode = policy.Mode
	return v, nil
}

// applyFailSemantics 对未决 verdict 应用失败语义。非 uncertain 原样通过；
// observe 策略永不 fail-close（observe 的定义就是只记录不拦截）。enforce
// 的未决：high 策略或全局开关 → 转 deny（fail-close）；否则放行并留
// intentgate.fail_open 结构化告警（验收 [unit] 的 warn 断言面）。
func (g *PolicyGate) applyFailSemantics(
	ctx context.Context, policy *types.IntentPolicy, in ToolCallInput, v Verdict,
) Verdict {
	if v.Action != ActionUncertain || policy.Mode != types.VerdictModeEnforce {
		return v
	}
	if policy.RiskTier == types.RiskTierHigh || g.failClose {
		v.Action = ActionDeny
		why := "高危策略故障即拦截（fail-close）"
		if g.failClose && policy.RiskTier != types.RiskTierHigh {
			why = "全局 fail-close 开关（" + FailCloseEnvVar + "）开启"
		}
		v.Reason = v.Reason + "；" + why
		logger.WarnWithFields(ctx, logger.Fields{
			"event":     "intentgate.fail_close",
			"tenant_id": in.TenantID,
			"policy_id": policy.ID,
			"tool":      in.ToolName,
			"risk_tier": policy.RiskTier,
		}, "[IntentGate] fail-close: uncertain escalated to deny")
		return v
	}
	logger.WarnWithFields(ctx, logger.Fields{
		"event":     "intentgate.fail_open",
		"tenant_id": in.TenantID,
		"policy_id": policy.ID,
		"tool":      in.ToolName,
	}, "[IntentGate] fail-open: uncertain passed in enforce mode")
	return v
}

// tenantCapabilityJudge 是可选接口：实现了 Enabled 的 judge（LLMJudge）
// 支持按租户能力档降级（T31）。未实现该接口的 judge 视为恒可用。
type tenantCapabilityJudge interface {
	Enabled(ctx context.Context, tenantID uint64) bool
}

// judgeEscalate 把判定升级给语义层（设计 §8.1 漏斗）。judge 未配置时
// 原样返回规则层的 uncertain（留"本该如何判"的观测数据）；judge 调用
// 失败按设计 §9 fail-open 记 uncertain，绝不上抛成判定链路错误。
// 租户模型弱于能力档时降级只跑规则层：verdict 保持规则层原值、layer
// 记 rule，并留一条结构化降级日志（T31 [cli] 验收面）。
// 升级时保留规则层的原始原因，便于事后对账"当初为什么进 judge"。
func (g *PolicyGate) judgeEscalate(
	ctx context.Context, policy *types.IntentPolicy, in ToolCallInput, ruleVerdict Verdict,
) Verdict {
	if g.judge == nil {
		return ruleVerdict
	}
	if tcj, ok := g.judge.(tenantCapabilityJudge); ok && !tcj.Enabled(ctx, in.TenantID) {
		// T31 降级：语义层不可用，规则层说什么就是什么。layer 统一记
		// rule（验收语义「降级只跑规则层并记 layer=rule」）；无 rule_expr
		// 的策略此前 layer=judge（evaluatePolicyRule 的标记），此处一并
		// 改记为 rule，避免降级后 layer 语义漂移。
		v := ruleVerdict
		v.Layer = LayerRule
		v.Reason = ruleVerdict.Reason + "；语义层降级（模型弱于 judge 能力档，只跑规则层，T31）"
		logger.WarnWithFields(ctx, logger.Fields{
			"event":     "intentgate.judge_degraded",
			"tenant_id": in.TenantID,
			"policy_id": policy.ID,
			"tool":      in.ToolName,
		}, "[IntentGate] judge degraded to rule layer (weak model tier)")
		return v
	}
	jv, err := g.judge.Judge(ctx, JudgeInput{
		TenantID:       in.TenantID,
		SessionID:      in.SessionID,
		PolicyID:       policy.ID,
		ConstraintText: policy.ConstraintText,
		ToolName:       in.ToolName,
		ServiceID:      in.ServiceID,
		Args:           in.Args,
		UserPrompt:     in.UserPrompt,
		History:        in.History,
	})
	if err != nil {
		return Verdict{
			Action: ActionUncertain,
			Layer:  LayerJudge,
			Reason: fmt.Sprintf("%s；judge 调用失败（fail-open）: %v", ruleVerdict.Reason, err),
		}
	}
	switch {
	case ruleVerdict.Reason != "" && jv.Reason != "":
		jv.Reason = ruleVerdict.Reason + "；judge: " + jv.Reason
	case ruleVerdict.Reason != "":
		jv.Reason = ruleVerdict.Reason
	}
	return jv
}

// baselineVerdict 无策略命中时的兜底判定：复用三条 spike 规则扫描
// 危险形态，layer=baseline（不是 rule——rule 层专属于策略 rule_expr
// 的判定，设计 §6.2 layer 枚举语义）。
func (g *PolicyGate) baselineVerdict(in ToolCallInput) Verdict {
	v := g.baseline.Evaluate(in)
	v.Layer = LayerBaseline
	return v
}

// evaluatePolicyRule 对命中策略做①规则层判定。rule_expr 表达的是约束
// 本身（"单笔退款不得超过 $75" → `value <= 75`）：求值为 true = 约束
// 满足 = allow，false = 约束被违反 = deny。
// RuleExprRequireApprovalSentinel 是 rule_expr 的显式哨兵值：整串等于
// "require_approval" 时，策略管辖的每次调用一律产出 require_approval
// verdict（LayerRule）——「本策略管辖的调用必须先经人工确认」是合法的
// 管理员诉求；同时它是 require_approval 链路（T41）的确定性验收触发器
//（issue #31：LLM judge 产出的 require_approval 无法稳定复现）。
const RuleExprRequireApprovalSentinel = "require_approval"

// evaluatePolicyRule 对命中策略做①规则层判定。rule_expr 表达的是约束
// 本身（"单笔退款不得超过 $75" → `value <= 75`）：求值为 true = 约束
// 满足 = allow，false = 约束被违反 = deny。哨兵值 require_approval 见
// RuleExprRequireApprovalSentinel。
func evaluatePolicyRule(policy *types.IntentPolicy, in ToolCallInput) Verdict {
	if policy.RuleExpr != nil && strings.TrimSpace(*policy.RuleExpr) == RuleExprRequireApprovalSentinel {
		return Verdict{
			Action: ActionRequireApproval,
			Layer:  LayerRule,
			Reason: "策略显式要求人工审批（rule_expr 哨兵 require_approval）",
		}
	}
	if policy.RuleExpr == nil || strings.TrimSpace(*policy.RuleExpr) == "" {
		// 无确定性表达式可判：约束只能走语义层（设计 §8.1 漏斗）。judge
		// 未接入（T30），记 uncertain——observe 放行并留下"本该如何判"
		// 的数据，正是 spike 要收集的东西。
		return Verdict{
			Action: ActionUncertain,
			Layer:  LayerJudge,
			Reason: "策略无 rule_expr，约束需语义层判定",
		}
	}
	compiled, err := CompileRuleExpr(*policy.RuleExpr)
	if err != nil {
		// 存量脏数据防御（创建入口暂未校验 rule_expr 可编译）：不得
		// panic、不得误判，记 uncertain。
		return Verdict{
			Action: ActionUncertain,
			Layer:  LayerRule,
			Reason: fmt.Sprintf("rule_expr 编译失败: %v", err),
		}
	}
	args := in.Args
	if policy.ArgPath != nil && strings.TrimSpace(*policy.ArgPath) != "" {
		// arg_path 抽取参数子值，rule_expr 的 value 别名指代该子值
		// （设计 §6.1：arg_path 为空表示整条调用）。
		args, err = extractArgPath(in.Args, *policy.ArgPath)
		if err != nil {
			return Verdict{
				Action: ActionUncertain,
				Layer:  LayerRule,
				Reason: fmt.Sprintf("arg_path %q 不适用: %v", *policy.ArgPath, err),
			}
		}
	}
	matched, err := compiled.Eval(args)
	if err != nil {
		// 不适用/超时（ErrRuleNotApplicable / ErrRuleTimeout）：按设计
		// §8.1 应升级语义层；judge 未接入，记 uncertain。
		return Verdict{
			Action: ActionUncertain,
			Layer:  LayerRule,
			Reason: fmt.Sprintf("rule_expr 对本次调用不适用（应升级语义层）: %v", err),
		}
	}
	if matched {
		return Verdict{
			Action: ActionAllow,
			Layer:  LayerRule,
			Reason: fmt.Sprintf("约束满足（rule_expr: %s）", compiled.Source()),
		}
	}
	return Verdict{
		Action: ActionDeny,
		Layer:  LayerRule,
		// deny 理由携带 NLC 原文：enforce 接线后（T40）agent 凭理由
		// 在对话中解释并自我纠错（设计决策「deny 走现有工具错误路径」）。
		Reason: fmt.Sprintf("违反策略约束「%s」（rule_expr: %s）", policy.ConstraintText, compiled.Source()),
	}
}

// extractArgPath 按 policy.arg_path（`$.a.b[0]` 形态）从 args 抽取子值，
// 重新序列化为 JSON 供 CompiledRule.Eval 求值（value 别名 = 该子值）。
// 路径缺失/类型不符/语法非法一律返回包裹 ErrRuleNotApplicable 的错误，
// 由调用方按"不适用"处置。本函数与 ruleexpr.go 的 pathNode 求值同语义，
// 但服务于「先抽取、再求值」的两段式（arg_path 抽取在策略层，路径
// 求值在表达式层），不复用其私有 AST。
func extractArgPath(args json.RawMessage, path string) (json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("%w: arg_path 必须以 $ 开头，got %q", ErrRuleNotApplicable, path)
	}
	var cur any
	if err := json.Unmarshal(args, &cur); err != nil {
		return nil, fmt.Errorf("%w: args 不是合法 JSON: %v", ErrRuleNotApplicable, err)
	}
	rest := strings.TrimPrefix(path, "$")
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			key := rest
			if end >= 0 {
				key = rest[:end]
			}
			if key == "" {
				return nil, fmt.Errorf("%w: arg_path %q 含空键名", ErrRuleNotApplicable, path)
			}
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: arg_path %q 的 .%s 目标不是对象", ErrRuleNotApplicable, path, key)
			}
			v, ok := obj[key]
			if !ok {
				return nil, fmt.Errorf("%w: arg_path %q 缺少键 %q", ErrRuleNotApplicable, path, key)
			}
			cur = v
			rest = rest[len(key):]
		case strings.HasPrefix(rest, "["):
			closeIdx := strings.Index(rest, "]")
			if closeIdx < 0 {
				return nil, fmt.Errorf("%w: arg_path %q 数组下标未闭合", ErrRuleNotApplicable, path)
			}
			var idx int
			if _, err := fmt.Sscanf(rest[1:closeIdx], "%d", &idx); err != nil {
				return nil, fmt.Errorf("%w: arg_path %q 数组下标非法", ErrRuleNotApplicable, path)
			}
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("%w: arg_path %q 下标 [%d] 越界或目标不是数组", ErrRuleNotApplicable, path, idx)
			}
			cur = arr[idx]
			rest = rest[closeIdx+1:]
		default:
			return nil, fmt.Errorf("%w: arg_path %q 含无法解析的段", ErrRuleNotApplicable, path)
		}
	}
	out, err := json.Marshal(cur)
	if err != nil {
		return nil, fmt.Errorf("%w: arg_path %q 抽取值无法序列化: %v", ErrRuleNotApplicable, path, err)
	}
	return out, nil
}

// 编译期断言：PolicyGate 实现 Gate 接口。
var _ Gate = (*PolicyGate)(nil)
