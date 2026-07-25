package chatpipeline

import (
	"context"
	"fmt"

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

	refs := feedbackReferenceRefs(chatManage, results)
	if len(refs) == 0 {
		pipelineInfo(ctx, "ChunkFeedbackRecorder", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_valid_chunk_ids",
		})
		return next()
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
		"chunk_count": len(refs),
	})

	return next()
}

func feedbackReferenceRefs(chatManage *types.ChatManage, results []*types.SearchResult) []*types.QAReplyChunkRef {
	if chatManage == nil {
		return nil
	}
	seen := make(map[string]struct{})
	refs := make([]*types.QAReplyChunkRef, 0, len(results))
	add := func(chunkID string, chunkTenantID uint64) {
		if chunkID == "" {
			return
		}
		if chunkTenantID == 0 {
			chunkTenantID = chatManage.TenantID
		}
		key := fmt.Sprintf("%d:%s", chunkTenantID, chunkID)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, &types.QAReplyChunkRef{
			MessageID:     chatManage.MessageID,
			ChunkID:       chunkID,
			TenantID:      chatManage.TenantID,
			ChunkTenantID: chunkTenantID,
		})
	}
	for _, result := range results {
		if !types.IsFeedbackTrackableSearchResult(result) {
			continue
		}
		chunkTenantID := chatManage.SearchTargets.GetTenantIDForKB(result.KnowledgeBaseID)
		add(result.ID, chunkTenantID)
		for _, subID := range result.SubChunkID {
			add(subID, chunkTenantID)
		}
	}
	return refs
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
