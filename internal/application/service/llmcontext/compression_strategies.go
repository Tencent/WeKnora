package llmcontext

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// slidingWindowStrategy implements CompressionStrategy using sliding window
type slidingWindowStrategy struct {
	recentMessageCount int
}

// NewSlidingWindowStrategy creates a new sliding window compression strategy
func NewSlidingWindowStrategy(recentMessageCount int) interfaces.CompressionStrategy {
	return &slidingWindowStrategy{
		recentMessageCount: recentMessageCount,
	}
}

// Compress implements the sliding window compression
// Keeps system messages and the most recent N messages
func (s *slidingWindowStrategy) Compress(
	ctx context.Context,
	messages []chat.Message,
	maxTokens int,
) ([]chat.Message, error) {
	if len(messages) <= s.recentMessageCount {
		return messages, nil
	}

	// Separate system messages from regular messages
	var systemMessages []chat.Message
	var regularMessages []chat.Message

	for _, msg := range messages {
		if msg.Role == "system" {
			systemMessages = append(systemMessages, msg)
		} else {
			regularMessages = append(regularMessages, msg)
		}
	}

	// Keep the most recent N regular messages
	var keptMessages []chat.Message
	if len(regularMessages) > s.recentMessageCount {
		keptMessages = regularMessages[len(regularMessages)-s.recentMessageCount:]
	} else {
		keptMessages = regularMessages
	}

	// Combine: system messages first, then recent messages
	result := make([]chat.Message, 0, len(systemMessages)+len(keptMessages))
	result = append(result, systemMessages...)
	result = append(result, keptMessages...)

	logger.Infof(ctx, "[SlidingWindow] Compressed %d messages to %d messages (kept %d recent + %d system)",
		len(messages), len(result), len(keptMessages), len(systemMessages))

	return result, nil
}

// EstimateTokens estimates token count (rough approximation: 4 characters ≈ 1 token)
func (s *slidingWindowStrategy) EstimateTokens(messages []chat.Message) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Role) + len(msg.Content)
		// Account for tool calls if present
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				totalChars += len(tc.Function.Name) + len(tc.Function.Arguments)
			}
		}
	}
	return totalChars / 4 // Rough approximation
}

// smartCompressionStrategy implements CompressionStrategy using LLM summarization
type smartCompressionStrategy struct {
	recentMessageCount int
	chatModel          chat.Chat
	summarizeThreshold int // Minimum messages before summarization
}

// NewSmartCompressionStrategy creates a new smart compression strategy
func NewSmartCompressionStrategy(
	recentMessageCount int,
	chatModel chat.Chat,
	summarizeThreshold int,
) interfaces.CompressionStrategy {
	return &smartCompressionStrategy{
		recentMessageCount: recentMessageCount,
		chatModel:          chatModel,
		summarizeThreshold: summarizeThreshold,
	}
}

// Compress implements smart compression with LLM summarization
// Summarizes old messages and keeps recent messages intact
func (s *smartCompressionStrategy) Compress(
	ctx context.Context,
	messages []chat.Message,
	maxTokens int,
) ([]chat.Message, error) {
	if len(messages) <= s.recentMessageCount {
		return messages, nil
	}

	// Separate system messages, old messages, and recent messages
	var systemMessages []chat.Message
	var oldMessages []chat.Message
	var recentMessages []chat.Message

	systemCount := 0
	for _, msg := range messages {
		if msg.Role == "system" {
			systemMessages = append(systemMessages, msg)
			systemCount++
		}
	}

	// Get regular messages (non-system)
	regularMessages := make([]chat.Message, 0, len(messages)-systemCount)
	for _, msg := range messages {
		if msg.Role != "system" {
			regularMessages = append(regularMessages, msg)
		}
	}

	// Split regular messages into old and recent
	if len(regularMessages) > s.recentMessageCount {
		splitPoint := len(regularMessages) - s.recentMessageCount
		oldMessages = regularMessages[:splitPoint]
		recentMessages = regularMessages[splitPoint:]
	} else {
		recentMessages = regularMessages
	}

	// If old messages are few, no need to summarize
	if len(oldMessages) < s.summarizeThreshold {
		result := make([]chat.Message, 0, len(systemMessages)+len(regularMessages))
		result = append(result, systemMessages...)
		result = append(result, regularMessages...)
		return result, nil
	}

	// Summarize old messages using LLM
	summary, err := s.summarizeMessages(ctx, oldMessages)
	if err != nil {
		logger.Warnf(ctx, "[SmartCompression] Failed to summarize messages: %v, falling back to old messages", err)
		// Fallback: return all messages if summarization fails
		result := make([]chat.Message, 0, len(systemMessages)+len(regularMessages))
		result = append(result, systemMessages...)
		result = append(result, regularMessages...)
		return result, nil
	}

	// Construct final message list: system + summary + recent
	result := make([]chat.Message, 0, len(systemMessages)+1+len(recentMessages))
	result = append(result, systemMessages...)
	result = append(result, chat.Message{
		Role:    "system",
		Content: fmt.Sprintf("Previous conversation summary:\n%s", summary),
	})
	result = append(result, recentMessages...)

	logger.Infof(
		ctx,
		"[SmartCompression] Compressed %d messages to %d messages (summarized %d old + kept %d recent + %d system)",
		len(messages),
		len(result),
		len(oldMessages),
		len(recentMessages),
		len(systemMessages),
	)

	return result, nil
}

// summarizeMessages uses LLM to create a summary of old messages
func (s *smartCompressionStrategy) summarizeMessages(ctx context.Context, messages []chat.Message) (string, error) {
	// Build conversation text
	var sb strings.Builder
	for i, msg := range messages {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, msg.Content))
		if i < len(messages)-1 {
			sb.WriteString("\n")
		}
	}

	languageDirective := buildLanguageDirectiveFromMessages(messages)
	systemContent := "You are a helpful assistant that summarizes conversations. " +
		"Provide a concise summary that captures the key points, decisions, and context. " +
		"Keep the summary brief but informative."
	systemContent = utils.AppendLanguageDirective(systemContent, languageDirective)

	summaryPrompt := []chat.Message{
		{
			Role:    "system",
			Content: systemContent,
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Please summarize the following conversation:\n\n%s", sb.String()),
		},
	}

	// Call LLM for summarization
	response, err := s.chatModel.Chat(ctx, summaryPrompt, &chat.ChatOptions{
		Temperature: 0.3, // Lower temperature for more consistent summaries
		MaxTokens:   500, // Limit summary length
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	if response == nil || response.Content == "" {
		return "", fmt.Errorf("no summary generated")
	}

	summary := response.Content
	logger.Debugf(ctx, "[SmartCompression] Generated summary (%d chars) from %d messages",
		len(summary), len(messages))

	return summary, nil
}

func buildLanguageDirectiveFromMessages(messages []chat.Message) string {
	for _, msg := range messages {
		if msg.Role == "user" && msg.Content != "" {
			return utils.BuildLanguageDirectiveFromText(msg.Content)
		}
	}
	return ""
}

// EstimateTokens estimates token count (rough approximation: 4 characters ≈ 1 token)
func (s *smartCompressionStrategy) EstimateTokens(messages []chat.Message) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Role) + len(msg.Content)
		// Account for tool calls if present
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				totalChars += len(tc.Function.Name) + len(tc.Function.Arguments)
			}
		}
	}
	return totalChars / 4 // Rough approximation
}
