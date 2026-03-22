// Package memory provides LLM-powered memory consolidation for agent conversations.
// When the context window grows too large, older messages are summarized by the LLM
// into a compact memory block that preserves key facts and tool results.
package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// DefaultConsolidationThreshold is the ratio of context window that triggers consolidation.
	// When token count exceeds MaxContextTokens * threshold, consolidation is triggered.
	DefaultConsolidationThreshold = 0.5

	// maxConsolidationAttempts is the maximum number of LLM calls for consolidation.
	// After this many failures, fall back to raw archiving.
	maxConsolidationAttempts = 3

	// consolidationTimeout is the timeout for the consolidation LLM call.
	consolidationTimeout = 60 * time.Second
)

// Consolidator compresses agent conversation history using LLM summarization.
// When the message history grows too large for the context window, it summarizes
// older messages into a compact system message that preserves key facts.
type Consolidator struct {
	chatModel chat.Chat
	estimator *token.Estimator
	maxTokens int     // context window size
	threshold float64 // trigger ratio (default 0.5)
}

// NewConsolidator creates a memory consolidator.
// maxContextTokens is the total context window budget.
// threshold is the ratio (0-1) at which consolidation triggers (0 = use default 0.5).
func NewConsolidator(
	chatModel chat.Chat,
	estimator *token.Estimator,
	maxContextTokens int,
	threshold float64,
) *Consolidator {
	if threshold <= 0 || threshold >= 1 {
		threshold = DefaultConsolidationThreshold
	}
	return &Consolidator{
		chatModel: chatModel,
		estimator: estimator,
		maxTokens: maxContextTokens,
		threshold: threshold,
	}
}

// ShouldConsolidate checks if consolidation is needed based on the caller-provided
// token estimate. The estimate should come from the model API's Usage when available.
func (c *Consolidator) ShouldConsolidate(currentTokens int) bool {
	if c.maxTokens <= 0 {
		return false
	}
	triggerAt := int(float64(c.maxTokens) * c.threshold)
	return currentTokens > triggerAt
}

// Consolidate summarizes older messages and returns a compressed message array.
// It preserves:
//   - The system prompt (first message)
//   - The current user query (last message)
//   - Recent messages that fit within the token budget
//
// Older messages are replaced with a summary system message.
// On LLM failure after maxConsolidationAttempts, falls back to raw text archiving.
func (c *Consolidator) Consolidate(
	ctx context.Context,
	messages []chat.Message,
) ([]chat.Message, error) {
	if len(messages) <= 3 {
		return messages, nil
	}

	systemMsg := messages[0]
	lastMsg := messages[len(messages)-1]
	middle := messages[1 : len(messages)-1]

	// Determine how many messages to keep vs. consolidate.
	// We want to consolidate enough to bring tokens below the threshold.
	targetTokens := int(float64(c.maxTokens) * c.threshold * 0.6) // aim for 60% of threshold
	keepFromEnd := c.findKeepBoundary(middle, targetTokens, &systemMsg, &lastMsg)

	if keepFromEnd >= len(middle) {
		// Nothing to consolidate
		return messages, nil
	}

	toConsolidate := middle[:len(middle)-keepFromEnd]
	toKeep := middle[len(middle)-keepFromEnd:]

	// Try LLM-powered summarization
	summary, err := c.summarizeWithRetry(ctx, toConsolidate)
	if err != nil {
		// Fall back to raw archiving
		logger.Warnf(ctx, "[MemoryConsolidator] LLM summarization failed after retries, "+
			"falling back to raw archive: %v", err)
		summary = c.rawArchive(toConsolidate)
	}

	// Build consolidated messages
	summaryMsg := chat.Message{
		Role: "system",
		Content: fmt.Sprintf(
			"[Memory Summary - %d earlier messages consolidated]\n\n%s",
			len(toConsolidate), summary,
		),
	}

	result := make([]chat.Message, 0, 3+len(toKeep))
	result = append(result, systemMsg)
	result = append(result, summaryMsg)
	result = append(result, toKeep...)
	result = append(result, lastMsg)

	logger.Infof(ctx, "[MemoryConsolidator] Consolidated %d messages → summary (%d chars), "+
		"keeping %d recent messages",
		len(toConsolidate), len(summary), len(toKeep))

	return result, nil
}

// findKeepBoundary determines how many messages from the end of middle to keep.
// Returns the count of messages to keep (from the end), respecting tool_call/tool_result boundaries.
func (c *Consolidator) findKeepBoundary(
	middle []chat.Message,
	targetTokens int,
	systemMsg, lastMsg *chat.Message,
) int {
	// Budget for kept messages = target - system - last
	budget := targetTokens -
		c.estimator.EstimateMessage(systemMsg) -
		c.estimator.EstimateMessage(lastMsg) -
		500 // reserve for summary message

	if budget <= 0 {
		return 0
	}

	// Walk from the end, accumulating tokens, respecting tool boundaries
	tokens := 0
	keepCount := 0
	i := len(middle) - 1

	for i >= 0 {
		msg := middle[i]
		msgTokens := c.estimator.EstimateMessage(&msg)

		// If this is a tool result, we must also keep its assistant message
		if msg.Role == "tool" {
			// Walk back to find the assistant message with tool_calls
			groupTokens := msgTokens
			groupSize := 1
			j := i - 1
			for j >= 0 && middle[j].Role == "tool" {
				groupTokens += c.estimator.EstimateMessage(&middle[j])
				groupSize++
				j--
			}
			// Include the assistant message
			if j >= 0 && middle[j].Role == "assistant" {
				groupTokens += c.estimator.EstimateMessage(&middle[j])
				groupSize++
			}

			if tokens+groupTokens > budget {
				break
			}
			tokens += groupTokens
			keepCount += groupSize
			i -= groupSize
		} else {
			if tokens+msgTokens > budget {
				break
			}
			tokens += msgTokens
			keepCount++
			i--
		}
	}

	return keepCount
}

// summarizeWithRetry attempts LLM summarization with retries.
func (c *Consolidator) summarizeWithRetry(
	ctx context.Context,
	messages []chat.Message,
) (string, error) {
	prompt := c.buildConsolidationPrompt(messages)
	var lastErr error

	for attempt := 1; attempt <= maxConsolidationAttempts; attempt++ {
		summarizeCtx, cancel := context.WithTimeout(ctx, consolidationTimeout)

		resp, err := c.chatModel.Chat(summarizeCtx, []chat.Message{
			{Role: "system", Content: consolidationSystemPrompt},
			{Role: "user", Content: prompt},
		}, &chat.ChatOptions{
			Temperature: 0.3, // low temperature for factual summarization
			MaxTokens:   2000,
		})
		cancel()

		if err != nil {
			lastErr = err
			logger.Warnf(ctx, "[MemoryConsolidator] Summarization attempt %d/%d failed: %v",
				attempt, maxConsolidationAttempts, err)
			continue
		}

		if resp != nil && resp.Content != "" {
			return resp.Content, nil
		}
		lastErr = fmt.Errorf("empty response from LLM")
	}

	return "", fmt.Errorf("summarization failed after %d attempts: %w",
		maxConsolidationAttempts, lastErr)
}

// buildConsolidationPrompt creates the prompt for LLM to summarize messages.
func (c *Consolidator) buildConsolidationPrompt(messages []chat.Message) string {
	var sb strings.Builder
	sb.WriteString("Summarize the following conversation history, preserving:\n")
	sb.WriteString("1. Key facts and decisions made\n")
	sb.WriteString("2. Tool execution results and their outcomes\n")
	sb.WriteString("3. User's original intent and requirements\n")
	sb.WriteString("4. Any errors encountered and how they were resolved\n\n")
	sb.WriteString("Conversation to summarize:\n\n")

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("**User**: %s\n\n", truncateForPrompt(msg.Content, 2000)))
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				names := make([]string, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					names[i] = tc.Function.Name
				}
				sb.WriteString(fmt.Sprintf("**Assistant** [called tools: %s]: %s\n\n",
					strings.Join(names, ", "), truncateForPrompt(msg.Content, 1000)))
			} else {
				sb.WriteString(fmt.Sprintf("**Assistant**: %s\n\n",
					truncateForPrompt(msg.Content, 2000)))
			}
		case "tool":
			sb.WriteString(fmt.Sprintf("**Tool [%s]**: %s\n\n",
				msg.Name, truncateForPrompt(msg.Content, 1000)))
		}
	}

	return sb.String()
}

// rawArchive creates a simple text dump of messages as fallback when LLM fails.
func (c *Consolidator) rawArchive(messages []chat.Message) string {
	var sb strings.Builder
	sb.WriteString("Raw conversation archive (LLM summarization unavailable):\n\n")

	for _, msg := range messages {
		content := truncateForPrompt(msg.Content, 500)
		switch msg.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("- User: %s\n", content))
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				names := make([]string, len(msg.ToolCalls))
				for j, tc := range msg.ToolCalls {
					names[j] = tc.Function.Name
				}
				sb.WriteString(fmt.Sprintf("- Assistant [tools: %s]: %s\n",
					strings.Join(names, ","), content))
			} else {
				sb.WriteString(fmt.Sprintf("- Assistant: %s\n", content))
			}
		case "tool":
			sb.WriteString(fmt.Sprintf("- Tool[%s]: %s\n", msg.Name, content))
		}
	}

	return sb.String()
}

// truncateForPrompt truncates a string to maxLen characters for use in prompts.
func truncateForPrompt(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

//nolint:lll // raw string literal used for prompt readability
const consolidationSystemPrompt = "" +
	"You are a conversation summarizer. " +
	"Your task is to create a concise but comprehensive summary " +
	"of a conversation between a user and an AI assistant.\n\n" +
	"The summary should:\n" +
	"- Be written in the same language as the original conversation\n" +
	"- Preserve all key facts, numbers, and specific details\n" +
	"- Include the outcomes of any tool executions\n" +
	"- Note any errors or issues encountered\n" +
	"- Be structured with clear sections if the conversation covered multiple topics\n" +
	"- Be concise — aim for 30% or less of the original length\n\n" +
	"Output only the summary, no preamble or explanation."

// ChatResponse is used to extract content from LLM response.
// This avoids a direct dependency on the types package for the response format.
var _ = (*types.ChatResponse)(nil) // compile-time check that types.ChatResponse exists
