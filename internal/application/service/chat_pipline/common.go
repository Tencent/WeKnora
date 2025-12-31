package chatpipline

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// pipelineInfo logs pipeline info level entries.
func pipelineInfo(ctx context.Context, stage, action string, fields map[string]interface{}) {
	common.PipelineInfo(ctx, stage, action, fields)
}

// pipelineWarn logs pipeline warning level entries.
func pipelineWarn(ctx context.Context, stage, action string, fields map[string]interface{}) {
	common.PipelineWarn(ctx, stage, action, fields)
}

// pipelineError logs pipeline error level entries.
func pipelineError(ctx context.Context, stage, action string, fields map[string]interface{}) {
	common.PipelineError(ctx, stage, action, fields)
}

// prepareChatModel shared logic to prepare chat model and options
// it gets the chat model and sets up the chat options based on the chat manage.
func prepareChatModel(ctx context.Context, modelService interfaces.ModelService,
	chatManage *types.ChatManage,
) (chat.Chat, *chat.ChatOptions, error) {
	chatModel, err := modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return nil, nil, err
	}

	opt := &chat.ChatOptions{
		Temperature:         chatManage.SummaryConfig.Temperature,
		TopP:                chatManage.SummaryConfig.TopP,
		Seed:                chatManage.SummaryConfig.Seed,
		MaxTokens:           chatManage.SummaryConfig.MaxTokens,
		MaxCompletionTokens: chatManage.SummaryConfig.MaxCompletionTokens,
		FrequencyPenalty:    chatManage.SummaryConfig.FrequencyPenalty,
		PresencePenalty:     chatManage.SummaryConfig.PresencePenalty,
		Thinking:            chatManage.SummaryConfig.Thinking,
	}

	return chatModel, opt, nil
}

// prepareMessagesWithHistory prepare complete messages including history
func prepareMessagesWithHistory(chatManage *types.ChatManage) []chat.Message {
	// Replace placeholders in system prompt
	systemPrompt := renderSystemPromptPlaceholders(chatManage.SummaryConfig.Prompt)
	
	chatMessages := []chat.Message{
		{Role: "system", Content: systemPrompt},
	}

	// Add conversation history (already limited by maxRounds in load_history/rewrite plugins)
	for _, history := range chatManage.History {
		chatMessages = append(chatMessages, chat.Message{Role: "user", Content: history.Query})
		chatMessages = append(chatMessages, chat.Message{Role: "assistant", Content: history.Answer})
	}

	// Add current user message
	chatMessages = append(chatMessages, chat.Message{Role: "user", Content: chatManage.UserContent})

	return chatMessages
}

// renderSystemPromptPlaceholders replaces placeholders in system prompt
// Supported placeholders:
//   - {{current_time}} -> current time in RFC3339 format
func renderSystemPromptPlaceholders(prompt string) string {
	result := prompt
	
	// Replace {{current_time}} placeholder
	if strings.Contains(result, "{{current_time}}") {
		currentTime := time.Now().Format(time.RFC3339)
		result = strings.ReplaceAll(result, "{{current_time}}", currentTime)
	}
	
	return result
}
