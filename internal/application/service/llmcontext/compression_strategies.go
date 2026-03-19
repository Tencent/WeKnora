package llmcontext

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// estimateStringTokens provides a more accurate token estimation by distinguishing
// CJK characters (≈1.5 tokens each) from Latin characters (≈4 chars per token).
// This is significantly more accurate than the naive totalChars/4 approach,
// especially for mixed Chinese-English content common in WeKnora.
func estimateStringTokens(s string) int {
	cjkChars := 0
	otherChars := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hiragana, r) {
			cjkChars++
		} else {
			otherChars++
		}
	}
	// CJK characters average ~1.5 tokens each; Latin ~0.25 tokens per char
	return (cjkChars*3 + otherChars) / 2
}

// estimateMessageTokens estimates the token count for a list of messages.
// Accounts for per-message overhead (role markers, special tokens) and tool call metadata.
func estimateMessageTokens(messages []chat.Message) int {
	totalTokens := 0
	for _, msg := range messages {
		totalTokens += estimateStringTokens(msg.Role) + estimateStringTokens(msg.Content)
		// Per-message overhead: role markers, delimiters, special tokens
		totalTokens += 4
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				totalTokens += estimateStringTokens(tc.Function.Name) + estimateStringTokens(tc.Function.Arguments)
				// Tool call overhead: function call structure tokens
				totalTokens += 8
			}
		}
	}
	return totalTokens
}

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

// EstimateTokens estimates token count using CJK-aware heuristics.
func (s *slidingWindowStrategy) EstimateTokens(messages []chat.Message) int {
	return estimateMessageTokens(messages)
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
		logger.Warnf(ctx, "[SmartCompression] Failed to summarize messages: %v, falling back to sliding window", err)
		// Fallback: use sliding window strategy to at least reduce message count
		fallback := &slidingWindowStrategy{recentMessageCount: s.recentMessageCount}
		return fallback.Compress(ctx, messages, maxTokens)
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
		fmt.Fprintf(&sb, "[%s] %s\n", msg.Role, msg.Content)
		if i < len(messages)-1 {
			sb.WriteString("\n")
		}
	}

	// Create summarization prompt
	summaryPrompt := []chat.Message{
		{
			Role: "system",
			Content: "You are a helpful assistant that summarizes conversations. " +
				"Provide a concise summary that captures the key points, decisions, and context. " +
				"Keep the summary brief but informative.",
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

// EstimateTokens estimates token count using CJK-aware heuristics.
func (s *smartCompressionStrategy) EstimateTokens(messages []chat.Message) int {
	return estimateMessageTokens(messages)
}
