package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ChunkFeedbackRecorder 片段反馈记录插件
// 在 INTO_CHAT_MESSAGE 阶段记录问答回复与知识库片段的关联关系，以便后续用户反馈时能够追踪到片段
type ChunkFeedbackRecorder struct {
	qaRefRepo interfaces.QAReplyChunkRefRepository
}

// NewChunkFeedbackRecorder 创建片段反馈记录插件
func NewChunkFeedbackRecorder(
	eventManager *EventManager,
	qaRefRepo interfaces.QAReplyChunkRefRepository,
) *ChunkFeedbackRecorder {
	recorder := &ChunkFeedbackRecorder{
		qaRefRepo: qaRefRepo,
	}
	eventManager.Register(recorder)
	return recorder
}

// ActivationEvents 返回此插件监听的事件类型
// 使用 INTO_CHAT_MESSAGE 事件，因为在此时已经完成了搜索和重排序，有最终的片段列表
func (p *ChunkFeedbackRecorder) ActivationEvents() []types.EventType {
	return []types.EventType{types.INTO_CHAT_MESSAGE}
}

// OnEvent 处理问答完成事件，记录回复与片段的关联
func (p *ChunkFeedbackRecorder) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	results, resultSet := feedbackReferenceResults(chatManage)
	if len(results) == 0 {
		pipelineInfo(ctx, "ChunkFeedbackRecorder", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_search_results",
		})
		return next()
	}

	// 检查是否有 assistant message ID
	if chatManage.MessageID == "" {
		pipelineWarn(ctx, "ChunkFeedbackRecorder", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_assistant_message_id",
		})
		return next()
	}

	// 提取片段 ID 列表
	chunkIDs := types.CollectSearchResultChunkIDs(results)
	if len(chunkIDs) == 0 {
		pipelineInfo(ctx, "ChunkFeedbackRecorder", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_valid_chunk_ids",
		})
		return next()
	}

	refs := make([]*types.QAReplyChunkRef, len(chunkIDs))
	for i, chunkID := range chunkIDs {
		refs[i] = &types.QAReplyChunkRef{
			MessageID: chatManage.MessageID,
			ChunkID:   chunkID,
			TenantID:  chatManage.TenantID,
		}
	}

	if err := p.qaRefRepo.CreateBatch(ctx, refs); err != nil {
		logger.Errorf(ctx, "Failed to save QA-Chunk refs for message %s: %v",
			chatManage.MessageID, err)
	} else {
		logger.Infof(ctx, "Saved %d QA-Chunk refs for message %s",
			len(refs), chatManage.MessageID)
	}

	pipelineInfo(ctx, "ChunkFeedbackRecorder", "recorded", map[string]interface{}{
		"session_id":  chatManage.SessionID,
		"message_id":  chatManage.MessageID,
		"result_set":  resultSet,
		"chunk_count": len(chunkIDs),
	})

	return next()
}

func feedbackReferenceResults(chatManage *types.ChatManage) ([]*types.SearchResult, string) {
	if len(chatManage.MergeResult) > 0 {
		return chatManage.MergeResult, "merge"
	}
	if len(chatManage.RerankResult) > 0 {
		return chatManage.RerankResult, "rerank"
	}
	return chatManage.SearchResult, "search"
}
