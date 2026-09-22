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
	"strings"

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
}

// PolicyGateOption 定制 PolicyGate（注入 judge 等）。
type PolicyGateOption func(*PolicyGate)

// WithJudge 装配语义层 judge（T30）。nil 表示语义层未配置：规则层
// 未决的判定保持 uncertain（与 T23 行为一致）。
func WithJudge(j Judge) PolicyGateOption {
	return func(g *PolicyGate) { g.judge = j }
}

// NewPolicyGate 创建 PolicyStore 驱动的 Gate。store 必须是与策略 CRUD
// handler 共享的同一实例——策略变更的 InvalidateTenant 才能生效
// （设计 §8.3：策略变更按 tenant 失效解析缓存）。
func NewPolicyGate(store PolicyStore, opts ...PolicyGateOption) *PolicyGate {
	g := &PolicyGate{store: store, baseline: NewSpikeRuleEngine()}
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
	case policy.RiskTier == types.RiskTierHigh:
		// 设计 §8.1：high 策略即使规则层判 allow 也强制进语义层复核——
		// 高危操作要语义兜底，不允许"规则没写全就当安全"。
		v = g.judgeEscalate(ctx, policy, in, v)
	case v.Action == ActionUncertain:
		// 规则层未决（无 rule_expr / 编译失败 / 不适用 / 超时）→ 语义层。
		v = g.judgeEscalate(ctx, policy, in, v)
	}
	v.PolicyID = policy.ID
	v.PolicyVersion = policy.Version
	v.Mode = policy.Mode
	return v, nil
}

// judgeEscalate 把判定升级给语义层（设计 §8.1 漏斗）。judge 未配置时
// 原样返回规则层的 uncertain（留"本该如何判"的观测数据）；judge 调用
// 失败按设计 §9 fail-open 记 uncertain，绝不上抛成判定链路错误。
// 升级时保留规则层的原始原因，便于事后对账"当初为什么进 judge"。
func (g *PolicyGate) judgeEscalate(
	ctx context.Context, policy *types.IntentPolicy, in ToolCallInput, ruleVerdict Verdict,
) Verdict {
	if g.judge == nil {
		return ruleVerdict
	}
	jv, err := g.judge.Judge(ctx, JudgeInput{
		TenantID:       in.TenantID,
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
func evaluatePolicyRule(policy *types.IntentPolicy, in ToolCallInput) Verdict {
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
