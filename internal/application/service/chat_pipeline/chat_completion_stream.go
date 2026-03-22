package chatpipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// PluginChatCompletionStream implements streaming chat completion functionality
// as a plugin that can be registered to EventManager
type PluginChatCompletionStream struct {
	modelService interfaces.ModelService // Interface for model operations
}

// NewPluginChatCompletionStream creates a new PluginChatCompletionStream instance
// and registers it with the EventManager
func NewPluginChatCompletionStream(eventManager *EventManager,
	modelService interfaces.ModelService,
) *PluginChatCompletionStream {
	res := &PluginChatCompletionStream{
		modelService: modelService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginChatCompletionStream) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHAT_COMPLETION_STREAM}
}

// OnEvent handles streaming chat completion events
// It prepares the chat model, messages, and initiates streaming response
func (p *PluginChatCompletionStream) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "Stream", "input", map[string]interface{}{
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

	// Prepare base messages without history

	chatMessages := prepareMessagesWithHistory(chatManage)
	pipelineInfo(ctx, "Stream", "messages_ready", map[string]interface{}{
		"message_count": len(chatMessages),
		"system_prompt": chatMessages[0].Content,
	})
	pipelineInfo(ctx, "Stream", "user_message", map[string]interface{}{
		"content": chatMessages[len(chatMessages)-1].Content,
	})
	// EventBus is required for event-driven streaming
	if chatManage.EventBus == nil {
		pipelineError(ctx, "Stream", "eventbus_missing", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return ErrModelCall.WithError(errors.New("EventBus is required for streaming"))
	}
	eventBus := chatManage.EventBus

	pipelineInfo(ctx, "Stream", "eventbus_ready", map[string]interface{}{
		"session_id": chatManage.SessionID,
	})

	// Initiate streaming chat model call with independent context
	pipelineInfo(ctx, "Stream", "model_call", map[string]interface{}{
		"chat_model": chatManage.ChatModelID,
	})
	responseChan, err := chatModel.ChatStream(ctx, chatMessages, opt)
	if err != nil {
		pipelineError(ctx, "Stream", "model_call", map[string]interface{}{
			"chat_model": chatManage.ChatModelID,
			"error":      err.Error(),
		})
		return ErrModelCall.WithError(err)
	}
	if responseChan == nil {
		pipelineError(ctx, "Stream", "model_call", map[string]interface{}{
			"chat_model": chatManage.ChatModelID,
			"error":      "nil_channel",
		})
		return ErrModelCall.WithError(errors.New("chat stream returned nil channel"))
	}

	pipelineInfo(ctx, "Stream", "model_started", map[string]interface{}{
		"session_id": chatManage.SessionID,
	})

	// Start goroutine to consume channel and emit events directly.
	// For non-agent mode, thinking content is embedded in answer stream with <think> tags.
	// The goroutine monitors ctx.Done() to avoid leaking when the context is cancelled
	// and the upstream channel is not closed promptly.
	go func() {
		answerID := fmt.Sprintf("%s-answer", uuid.New().String()[:8])
		var finalContent string
		var thinkingStarted bool
		var thinkingEnded bool

		for {
			select {
			case <-ctx.Done():
				if thinkingStarted && !thinkingEnded {
					eventBus.Emit(ctx, types.Event{
						ID:        answerID,
						Type:      types.EventType(event.EventAgentFinalAnswer),
						SessionID: chatManage.SessionID,
						Data: event.AgentFinalAnswerData{
							Content: "</think>",
							Done:    true,
						},
					})
				}
				pipelineInfo(ctx, "Stream", "context_cancelled", map[string]interface{}{
					"session_id": chatManage.SessionID,
				})
				return

			case response, ok := <-responseChan:
				if !ok {
					if thinkingStarted && !thinkingEnded {
						finalContent += "</think>"
						eventBus.Emit(ctx, types.Event{
							ID:        answerID,
							Type:      types.EventType(event.EventAgentFinalAnswer),
							SessionID: chatManage.SessionID,
							Data: event.AgentFinalAnswerData{
								Content: "</think>",
								Done:    true,
							},
						})
					}
					pipelineInfo(ctx, "Stream", "channel_close", map[string]interface{}{
						"session_id": chatManage.SessionID,
					})
					return
				}

				if response.ResponseType == types.ResponseTypeError {
					pipelineError(ctx, "Stream", "stream_error", map[string]interface{}{
						"session_id": chatManage.SessionID,
						"error":      response.Content,
					})
					eventBus.Emit(ctx, types.Event{
						ID:        fmt.Sprintf("%s-error", uuid.New().String()[:8]),
						Type:      types.EventType(event.EventError),
						SessionID: chatManage.SessionID,
						Data: event.ErrorData{
							Error:     response.Content,
							Stage:     "chat_completion_stream",
							SessionID: chatManage.SessionID,
						},
					})
					continue
				}

				if response.ResponseType == types.ResponseTypeThinking {
					content := response.Content
					if !thinkingStarted {
						content = "<think>" + content
						thinkingStarted = true
					}
					if response.Done && !thinkingEnded {
						content = content + "</think>"
						thinkingEnded = true
					}
					finalContent += content
					eventBus.Emit(ctx, types.Event{
						ID:        answerID,
						Type:      types.EventType(event.EventAgentFinalAnswer),
						SessionID: chatManage.SessionID,
						Data: event.AgentFinalAnswerData{
							Content: content,
							Done:    false,
						},
					})
					continue
				}

				if response.ResponseType == types.ResponseTypeAnswer {
					if thinkingStarted && !thinkingEnded {
						thinkingEnded = true
						finalContent += "</think>"
						eventBus.Emit(ctx, types.Event{
							ID:        answerID,
							Type:      types.EventType(event.EventAgentFinalAnswer),
							SessionID: chatManage.SessionID,
							Data: event.AgentFinalAnswerData{
								Content: "</think>",
								Done:    false,
							},
						})
					}
					finalContent += response.Content
					eventBus.Emit(ctx, types.Event{
						ID:        answerID,
						Type:      types.EventType(event.EventAgentFinalAnswer),
						SessionID: chatManage.SessionID,
						Data: event.AgentFinalAnswerData{
							Content: response.Content,
							Done:    response.Done,
						},
					})
				}
			}
		}
	}()

	return next()
}
