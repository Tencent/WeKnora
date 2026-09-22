// IntentGate 语义层（judge）的模型解析（T30/T31，issue #14/#15）。
//
// 生产接线把 newJudgeModelResolver 注入 intentgate.NewLLMJudge：每次
// judge 调用前按租户解析一个 chat 模型实例。resolver 错误（租户未配
// chat 模型等）由 LLMJudge 转为 uncertain（fail-open，设计 §9），
// 这里只管"怎么选"。
package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newJudgeModelResolver 返回 intentgate.JudgeModelResolver 的生产实现：
// 在租户自己的模型清单里选 chat 模型（ModelTypeKnowledgeQA 即前端所称
// chat 模型）供 judge 使用。
//
// 选取策略（T31）：能力档达标的模型优先（JudgeCapable，弱档模型默认
// 不做 judge 候选——选了也会在 Enabled 处被降级拦下，白白多一次解析）；
// 同档内按 ID 排序取第一个，确定性可复现。设计 §12 说"默认走该租户已配
// 的最便宜 chat 模型"：租户模型配置里没有价格字段，按价格精化选择
// 留待后续（届时替换 candidates 排序段即可）。
func newJudgeModelResolver(modelService interfaces.ModelService) intentgate.JudgeModelResolver {
	return func(ctx context.Context, tenantID uint64) (*intentgate.ResolvedJudgeModel, error) {
		// ListModels/GetChatModel 都从 ctx 取租户（MustTenantIDFromContext），
		// 判定上下文里的租户即策略租户，直接换到目标租户视角。
		tctx := types.WithExecutionTenant(ctx, tenantID)
		models, err := modelService.ListModels(tctx)
		if err != nil {
			return nil, fmt.Errorf("list tenant %d models: %w", tenantID, err)
		}
		candidates := make([]*types.Model, 0, len(models))
		for _, m := range models {
			if m == nil || m.Type != types.ModelTypeKnowledgeQA {
				continue
			}
			if m.Status == types.ModelStatusDownloadFailed {
				continue
			}
			candidates = append(candidates, m)
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("tenant %d has no chat model for intentgate judge", tenantID)
		}
		// 强档优先、同档按 ID 序（稳定、可复现）。
		sort.SliceStable(candidates, func(i, j int) bool {
			ci, cj := intentgate.JudgeCapable(candidates[i]), intentgate.JudgeCapable(candidates[j])
			if ci != cj {
				return ci
			}
			return candidates[i].ID < candidates[j].ID
		})
		picked := candidates[0]
		chatModel, err := modelService.GetChatModel(tctx, picked.ID)
		if err != nil {
			return nil, fmt.Errorf("get judge chat model %s for tenant %d: %w", picked.ID, tenantID, err)
		}
		return &intentgate.ResolvedJudgeModel{Chat: chatModel, Model: picked}, nil
	}
}
