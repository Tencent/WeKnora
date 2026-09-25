package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// emitMemoryRecalled tells the client which memories this turn saw. Best
// effort: an event failure must not stop the answer.
func emitMemoryRecalled(
	ctx context.Context, bus types.EventBusInterface, sessionID string, used types.UsedMemories,
) {
	if bus == nil || len(used) == 0 {
		return
	}
	if err := bus.Emit(ctx, types.Event{
		Type:      types.EventType(event.EventMemoryRecalled),
		SessionID: sessionID,
		Data:      event.MemoryRecalledData{Memories: used},
	}); err != nil {
		pipelineWarn(ctx, "MemoryRecall", "emit_failed", map[string]interface{}{"error": err.Error()})
	}
}

// PluginMemoryRecall injects the user's long-term memory into the turn.
//
// It runs after LOAD_HISTORY and before retrieval so the memory is available
// to every downstream stage, and it performs no model call: recall is a
// primary-key read for the resident block plus lexical matching over a few
// hundred short rows. A turn's first token must not wait on memory.
type PluginMemoryRecall struct {
	memoryService interfaces.MemoryService
}

func NewPluginMemoryRecall(
	eventManager *EventManager,
	memoryService interfaces.MemoryService,
) *PluginMemoryRecall {
	res := &PluginMemoryRecall{memoryService: memoryService}
	eventManager.Register(res)
	return res
}

func (p *PluginMemoryRecall) ActivationEvents() []types.EventType {
	return []types.EventType{types.MEMORY_RECALL}
}

func (p *PluginMemoryRecall) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	if p.memoryService == nil {
		pipelineInfo(ctx, "MemoryRecall", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "no_service",
		})
		return next()
	}

	pipelineInfo(ctx, "MemoryRecall", "input", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"query_len":     len(chatManage.Query),
		"query_preview": langfuse.TruncateRunes(chatManage.Query, 200),
	})

	recall := p.memoryService.Recall(ctx, chatManage.Query)
	if recall.Prompt == "" {
		pipelineInfo(ctx, "MemoryRecall", "output", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"items":      0,
			"injected":   false,
			"note":       "interest memories apply in query_understand, not here",
		})
		return next()
	}

	chatManage.MemoryPrompt = recall.Prompt
	chatManage.UsedMemories = types.UsedMemoriesFromItems(recall.Items)
	emitMemoryRecalled(ctx, chatManage.EventBus, chatManage.SessionID, chatManage.UsedMemories)

	memoryIDs := make([]string, 0, len(recall.Items))
	for _, item := range recall.Items {
		if item != nil && item.ID != "" {
			memoryIDs = append(memoryIDs, item.ID)
		}
	}
	pipelineInfo(ctx, "MemoryRecall", "output", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"items":         len(chatManage.UsedMemories),
		"injected":      true,
		"prompt_runes":  len([]rune(recall.Prompt)),
		"memory_ids":    memoryIDs,
		"query_preview": langfuse.TruncateRunes(chatManage.Query, 200),
	})
	return next()
}
