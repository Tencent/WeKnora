// PolicyGate：PolicyStore 驱动的 Gate 实现（issue #13 / T23，设计 §7）。
//
// 判定输入从硬编码规则切换为读策略库：
//  1. PolicyStore.Resolve 解析本次调用命中的最具体策略（scope 顺序与
//     per-tenant 缓存见 policy_store.go，设计 §8.3）；
//  2. 命中策略且有 rule_expr → ① 确定性规则层判定（编译失败/不适用/
//     超时一律记 uncertain 升级语义层，绝不静默放行或误拦）；
//  3. 命中策略但无 rule_expr → 约束只能由语义层判定，judge 未接入
//     （T30），记 uncertain、layer=judge；
//  4. 无策略命中 → baseline 判定：复用 spike 规则做危险形态兜底扫描，
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

// PolicyGate 是设计 §7 Gate 结构的当前形态：store（策略读取）已接入，
// judge（语义层）待 T30。baseline 规则引擎在无策略命中时兜底。
//
// 并发安全：PolicyGate 本身无状态（策略解析状态都在 PolicyStore 内），
// 单实例可安全地被全部 engine 共享。
type PolicyGate struct {
	store    PolicyStore
	baseline *RuleEngine
}

// NewPolicyGate 创建 PolicyStore 驱动的 Gate。store 必须是与策略 CRUD
// handler 共享的同一实例——策略变更的 InvalidateTenant 才能生效
// （设计 §8.3：策略变更按 tenant 失效解析缓存）。
func NewPolicyGate(store PolicyStore) *PolicyGate {
	return &PolicyGate{store: store, baseline: NewSpikeRuleEngine()}
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
	v.PolicyID = policy.ID
	v.PolicyVersion = policy.Version
	v.Mode = policy.Mode
	return v, nil
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
			Reason: "策略无 rule_expr，约束需语义层判定（judge 未接入，T30）",
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
			Reason: fmt.Sprintf("rule_expr 对本次调用不适用（应升级语义层，judge 未接入 T30）: %v", err),
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
