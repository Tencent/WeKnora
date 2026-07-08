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
	if chatManage.AssistantMessageID == "" {
		pipelineWarn(ctx, "ChunkFeedbackRecorder", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_assistant_message_id",
		})
		return next()
	}

	// 提取片段 ID 列表
	chunkIDs := make([]string, 0, len(results))
	seen := make(map[string]bool)
	for _, result := range results {
		if result.ID != "" && !seen[result.ID] {
			chunkIDs = append(chunkIDs, result.ID)
			seen[result.ID] = true
		}
	}

	if len(chunkIDs) == 0 {
		pipelineInfo(ctx, "ChunkFeedbackRecorder", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_valid_chunk_ids",
		})
		return next()
	}

	// 异步记录关联关系（不阻塞流程）
	go func() {
		bgCtx := context.Background()
		refs := make([]*types.QAReplyChunkRef, len(chunkIDs))
		for i, chunkID := range chunkIDs {
			refs[i] = &types.QAReplyChunkRef{
				MessageID: chatManage.AssistantMessageID,
				ChunkID:   chunkID,
				TenantID:  chatManage.TenantID,
			}
		}

		if err := p.qaRefRepo.CreateBatch(bgCtx, refs); err != nil {
			logger.Errorf(bgCtx, "Failed to save QA-Chunk refs for message %s: %v",
				chatManage.AssistantMessageID, err)
		} else {
			logger.Infof(bgCtx, "Saved %d QA-Chunk refs for message %s",
				len(refs), chatManage.AssistantMessageID)
		}
	}()

	pipelineInfo(ctx, "ChunkFeedbackRecorder", "recorded", map[string]interface{}{
		"session_id":  chatManage.SessionID,
		"message_id":  chatManage.AssistantMessageID,
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
