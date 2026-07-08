package chatpipeline

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

// RecallWeightApplier 召回权重应用插件
// 在 CHUNK_RERANK 阶段后置应用片段的召回权重，确保合并和 TopK 过滤使用调整后的分数。
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
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent 处理 CHUNK_RERANK 事件，在权重加载完成后应用召回权重。
func (p *RecallWeightApplier) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if !chatManage.NeedsRetrieval() {
		return next()
	}
	if err := next(); err != nil {
		return err
	}

	results := chatManage.RerankResult
	resultSet := "rerank"
	if len(results) == 0 {
		results = chatManage.SearchResult
		resultSet = "search"
	}

	applied := 0
	for _, result := range results {
		if result.RecallWeight == 0 || result.RecallWeight == 1.0 {
			continue
		}
		originalScore := result.Score
		result.Score *= result.RecallWeight
		result.Metadata = ensureMetadata(result.Metadata)
		result.Metadata["recall_weight"] = fmt.Sprintf("%.2f", result.RecallWeight)
		result.Metadata["recall_weight_original_score"] = fmt.Sprintf("%.4f", originalScore)
		result.Metadata["recall_weighted_score"] = fmt.Sprintf("%.4f", result.Score)
		applied++
	}

	if applied > 0 {
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})
		pipelineInfo(ctx, "RecallWeightApplier", "applied", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"result_set": resultSet,
			"affected":   applied,
			"total":      len(results),
		})
	}

	return nil
}
