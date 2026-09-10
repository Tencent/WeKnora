package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginChatCompletion implements chat completion functionality
// as a plugin that can be registered to EventManager
type PluginChatCompletion struct {
	modelService interfaces.ModelService // Interface for model operations
}

// NewPluginChatCompletion creates a new PluginChatCompletion instance
// and registers it with the EventManager
func NewPluginChatCompletion(eventManager *EventManager, modelService interfaces.ModelService) *PluginChatCompletion {
	res := &PluginChatCompletion{
		modelService: modelService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginChatCompletion) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHAT_COMPLETION}
}

// OnEvent handles chat completion events
// It prepares the chat model, messages, and calls the model to generate responses
func (p *PluginChatCompletion) OnEvent(
	ctx context.Context, eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "Completion", "input", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"user_question":  chatManage.UserContent,
		"history_rounds": len(chatManage.History),
		"chat_model":     chatManage.ChatModelID,
	})

	// Prepare chat model and options
	chatModel, opt, err := prepareChatModel(ctx, p.modelService, chatManage)
	if err != nil {
		return ErrGetChatModel.WithError(err)
	}

	// Prepare messages including conversation history
	pipelineInfo(ctx, "Completion", "messages_ready", map[string]interface{}{
		"message_count": len(chatManage.History) + 2,
	})
	chatMessages, modelContext := prepareMessagesWithModelContext(ctx, chatManage)
	chatMessages = modelContext.EncodeMessages(chatMessages)
	ctx = withPromptCacheMetadata(ctx, chatModel, chatMessages, opt, "knowledge_qa")

	// Call the chat model to generate response
	pipelineInfo(ctx, "Completion", "model_call", map[string]interface{}{
		"chat_model": chatManage.ChatModelID,
	})
	chatResponse, err := invoke.Chat(ctx, chatModel, opt)
	if err != nil {
		pipelineError(ctx, "Completion", "model_call", map[string]interface{}{
			"chat_model": chatManage.ChatModelID,
			"error":      err.Error(),
		})
		return ErrModelCall.WithError(err)
	}
	typesResponse := chatResponse.ToTypes()
	modelContext.DecodeResponse(typesResponse)
	if orphans := modelContext.OrphanResourceHandles(typesResponse.Content); len(orphans) > 0 {
		pipelineWarn(ctx, "Completion", "orphan_resource_handles", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"handles":    orphans,
		})
	}

	pipelineInfo(ctx, "Completion", "output", map[string]interface{}{
		"answer_preview":    typesResponse.Content,
		"finish_reason":     typesResponse.FinishReason,
		"completion_tokens": typesResponse.Usage.CompletionTokens,
		"prompt_tokens":     typesResponse.Usage.PromptTokens,
	})
	chatManage.ChatResponse = typesResponse
	return next()
}
