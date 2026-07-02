package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// RecallWeightApplier 召回权重应用插件
// 在 INTO_CHAT_MESSAGE 阶段应用片段的召回权重来调整搜索结果的分数
// 高质量片段（好评率高）权重提升，低质量片段权重降低
type RecallWeightApplier struct{}

// NewRecallWeightApplier 创建召回权重应用插件
func NewRecallWeightApplier(eventManager *EventManager) *RecallWeightApplier {
	applier := &RecallWeightApplier{}
	eventManager.Register(applier)
	return applier
}

// ActivationEvents 返回此插件监听的事件类型
func (p *RecallWeightApplier) ActivationEvents() []types.EventType {
	return []types.EventType{types.INTO_CHAT_MESSAGE}
}

// OnEvent 处理 INTO_CHAT_MESSAGE 事件，应用召回权重
func (p *RecallWeightApplier) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// 应用权重
	applied := 0
	for _, result := range chatManage.SearchResult {
		if result.RecallWeight != 0 && result.RecallWeight != 1.0 {
			originalScore := result.Score
			result.Score *= result.RecallWeight
			applied++
			logger.Debugf(ctx, "Applied recall weight %.2f to chunk %s: %.4f -> %.4f",
				result.RecallWeight, result.ID, originalScore, result.Score)
		}
	}

	if applied > 0 {
		pipelineInfo(ctx, "RecallWeightApplier", "applied", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"affected":   applied,
			"total":      len(chatManage.SearchResult),
		})
	}

	return next()
}
