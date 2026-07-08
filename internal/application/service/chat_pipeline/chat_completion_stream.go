package chatpipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	// reasoning_content is routed to EventAgentThought (SSE response_type=thinking)
	// and plain answer text to EventAgentFinalAnswer, matching the Agent pipeline.
	// The goroutine monitors ctx.Done() to avoid leaking when the context is cancelled
	// and the upstream channel is not closed promptly.
	go func() {
		thinkingID := fmt.Sprintf("%s-thinking", uuid.New().String()[:8])
		answerID := fmt.Sprintf("%s-answer", uuid.New().String()[:8])
		thinkingOpen := false
		var answerBuilder strings.Builder
		answerDone := false
		terminalErrorEmitted := false

		closeThinking := func() {
			if !thinkingOpen {
				return
			}
			eventBus.Emit(ctx, types.Event{
				ID:        thinkingID,
				Type:      types.EventType(event.EventAgentThought),
				SessionID: chatManage.SessionID,
				Data: event.AgentThoughtData{
					Done: true,
				},
			})
			thinkingOpen = false
		}

		emitTerminalError := func(errorCode, detail string) {
			if terminalErrorEmitted {
				return
			}
			terminalErrorEmitted = true
			closeThinking()
			userMessage := streamErrorUserMessage(chatManage.Language, answerBuilder.Len() > 0)
			eventBus.Emit(ctx, types.Event{
				ID:        fmt.Sprintf("%s-error", uuid.New().String()[:8]),
				Type:      types.EventType(event.EventError),
				SessionID: chatManage.SessionID,
				Data: event.ErrorData{
					Error:     userMessage,
					ErrorCode: errorCode,
					Stage:     "chat_completion_stream",
					SessionID: chatManage.SessionID,
					Extra: map[string]interface{}{
						"detail":   detail,
						"terminal": true,
					},
				},
			})
		}

		for {
			select {
			case <-ctx.Done():
				closeThinking()
				pipelineInfo(ctx, "Stream", "context_cancelled", map[string]interface{}{
					"session_id": chatManage.SessionID,
				})
				return

			case response, ok := <-responseChan:
				if !ok {
					closeThinking()
					pipelineInfo(ctx, "Stream", "channel_close", map[string]interface{}{
						"session_id": chatManage.SessionID,
					})
					if !answerDone && !terminalErrorEmitted && ctx.Err() == nil {
						pipelineWarn(ctx, "Stream", "channel_closed_without_done", map[string]interface{}{
							"session_id": chatManage.SessionID,
							"answer_len": answerBuilder.Len(),
						})
						emitTerminalError("chat_stream_closed", "model stream closed before sending a done event")
					}
					return
				}

				if response.ResponseType == types.ResponseTypeError {
					pipelineError(ctx, "Stream", "stream_error", map[string]interface{}{
						"session_id": chatManage.SessionID,
						"error":      response.Content,
					})
					emitTerminalError("chat_stream_error", response.Content)
					return
				}

				if response.ResponseType == types.ResponseTypeThinking {
					if response.Content != "" {
						thinkingOpen = true
						eventBus.Emit(ctx, types.Event{
							ID:        thinkingID,
							Type:      types.EventType(event.EventAgentThought),
							SessionID: chatManage.SessionID,
							Data: event.AgentThoughtData{
								Content: response.Content,
								Done:    false,
							},
						})
					}
					if response.Done {
						closeThinking()
					}
					continue
				}

				if response.ResponseType == types.ResponseTypeAnswer {
					closeThinking()
					if response.Content != "" {
						answerBuilder.WriteString(response.Content)
					}
					if response.Done && strings.TrimSpace(answerBuilder.String()) == "" {
						pipelineWarn(ctx, "Stream", "empty_answer_done", map[string]interface{}{
							"session_id":    chatManage.SessionID,
							"finish_reason": response.FinishReason,
						})
						emitTerminalError("chat_stream_empty_answer", "model stream finished without answer content")
						return
					}
					if response.Done {
						answerDone = true
						warnIfAnswerMissingKBCitations(ctx, "Stream", chatManage, answerBuilder.String())
					}
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

func streamErrorUserMessage(language string, hasPartialAnswer bool) string {
	if strings.Contains(strings.ToLower(language), "chinese") || strings.HasPrefix(strings.ToLower(language), "zh") {
		if hasPartialAnswer {
			return "回答生成中断：模型服务暂时不可用或连接中断，请稍后重试。"
		}
		return "回答生成失败：模型服务暂时不可用或连接中断，请稍后重试。"
	}
	if hasPartialAnswer {
		return "Answer generation was interrupted because the model service was unavailable or the connection was closed. Please try again later."
	}
	return "Answer generation failed because the model service was unavailable or the connection was closed. Please try again later."
}
