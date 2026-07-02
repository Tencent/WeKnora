package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkWeightLoader 片段权重加载插件
// 在 CHUNK_RERANK 之后、INTO_CHAT_MESSAGE 之前加载片段的召回权重到搜索结果中
type ChunkWeightLoader struct {
	chunkRepo repository.ChunkRepository
}

// NewChunkWeightLoader 创建片段权重加载插件
func NewChunkWeightLoader(
	eventManager *EventManager,
	chunkRepo repository.ChunkRepository,
) *ChunkWeightLoader {
	loader := &ChunkWeightLoader{
		chunkRepo: chunkRepo,
	}
	eventManager.Register(loader)
	return loader
}

// ActivationEvents 返回此插件监听的事件类型
// 在重排序之后加载，以便权重可以立即被应用
func (p *ChunkWeightLoader) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent 处理 CHUNK_RERANK 事件后，加载片段权重
func (p *ChunkWeightLoader) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// 首先执行后续插件
	if err := next(); err != nil {
		return err
	}

	// 加载片段权重
	if len(chatManage.SearchResult) == 0 {
		return nil
	}

	// 提取 chunk IDs
	chunkIDs := make([]string, 0, len(chatManage.SearchResult))
	for _, result := range chatManage.SearchResult {
		if result.ID != "" {
			chunkIDs = append(chunkIDs, result.ID)
		}
	}

	// 批量获取片段信息（包含权重）
	chunks, err := p.chunkRepo.ListChunksByID(ctx, chatManage.TenantID, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to load chunk weights: %v", err)
		return nil
	}

	// 构建 chunk ID -> recall_weight 的映射
	weightMap := make(map[string]float64)
	for _, chunk := range chunks {
		weightMap[chunk.ID] = chunk.RecallWeight
	}

	// 应用权重到搜索结果
	loaded := 0
	for _, result := range chatManage.SearchResult {
		if weight, ok := weightMap[result.ID]; ok {
			result.RecallWeight = weight
			loaded++
		} else {
			result.RecallWeight = 1.0 // 默认权重
		}
	}

	if loaded > 0 {
		pipelineInfo(ctx, "ChunkWeightLoader", "loaded", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"loaded":     loaded,
			"total":      len(chatManage.SearchResult),
		})
	}

	return nil
}
