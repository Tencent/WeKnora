package chatpipline

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type MemoryPlugin struct {
	memoryService interfaces.MemoryService
}

func NewMemoryPlugin(eventManager *EventManager, memoryService interfaces.MemoryService) *MemoryPlugin {
	res := &MemoryPlugin{
		memoryService: memoryService,
	}
	eventManager.Register(res)
	return res
}

func (p *MemoryPlugin) ActivationEvents() []types.EventType {
	return []types.EventType{
		types.MEMORY_RETRIEVAL,
		types.MEMORY_STORAGE,
	}
}

func (p *MemoryPlugin) OnEvent(
	ctx context.Context,
	eventType types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	switch eventType {
	case types.MEMORY_RETRIEVAL:
		return p.handleRetrieval(ctx, chatManage, next)
	case types.MEMORY_STORAGE:
		return p.handleStorage(ctx, chatManage, next)
	default:
		return next()
	}
}

func (p *MemoryPlugin) handleRetrieval(
	ctx context.Context,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	// Check if memory is enabled
	if !chatManage.EnableMemory {
		return next()
	}
	logger.Info(ctx, "Start to retrieve memory")

	// Retrieve memory context
	query := chatManage.RewriteQuery
	if query == "" {
		query = chatManage.Query
	}

	memoryContext, err := p.memoryService.RetrieveMemory(ctx, chatManage.UserID, query)
	if err != nil {
		logger.Errorf(ctx, "failed to retrieve memory: %v", err)
		// Don't block the pipeline if memory retrieval fails
		return next()
	}

	// Add memory context to chatManage
	if len(memoryContext.RelatedEpisodes) > 0 {
		memoryStr := "\n\nRelevant Memory:\n"
		for _, ep := range memoryContext.RelatedEpisodes {
			memoryStr += fmt.Sprintf("- %s (Summary: %s)\n", ep.CreatedAt.Format("2006-01-02"), ep.Summary)
		}
		chatManage.UserContent += memoryStr
		logger.Info(ctx, "Retrieved memory: %s", memoryStr)
	}
	logger.Info(ctx, "End to retrieve memory")

	return next()
}

func (p *MemoryPlugin) handleStorage(
	ctx context.Context,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	if err := next(); err != nil {
		return err
	}

	// Check if memory is enabled
	if !chatManage.EnableMemory {
		return nil
	}

	logger.Info(ctx, "Start to store memory")
	// If ChatResponse is already available (non-streaming), store it directly
	if chatManage.ChatResponse != nil {
		messages := []types.Message{
			{Role: "user", Content: chatManage.Query},
			{Role: "assistant", Content: chatManage.ChatResponse.Content},
		}
		// Capture UserID and SessionID for goroutine
		userID := chatManage.UserID
		sessionID := chatManage.SessionID
		go func() {
			if err := p.memoryService.AddEpisode(ctx, userID, sessionID, messages); err != nil {
				logger.Errorf(ctx, "failed to add episode: %v", err)
			}
		}()
		return nil
	}

	// If streaming, subscribe to events
	if chatManage.EventBus != nil {
		var fullResponse string
		// Capture UserID and SessionID for closure
		userID := chatManage.UserID
		sessionID := chatManage.SessionID

		chatManage.EventBus.On(types.EventType(event.EventAgentFinalAnswer), func(ctx context.Context, evt types.Event) error {
			data, ok := evt.Data.(event.AgentFinalAnswerData)
			if !ok {
				return nil
			}
			fullResponse += data.Content
			if data.Done {
				messages := []types.Message{
					{Role: "user", Content: chatManage.Query},
					{Role: "assistant", Content: fullResponse},
				}
				go func() {
					if err := p.memoryService.AddEpisode(ctx, userID, sessionID, messages); err != nil {
						logger.Errorf(ctx, "failed to add episode: %v", err)
					}
				}()
			}
			return nil
		})
	}
	logger.Info(ctx, "End to store memory")

	return nil
}
