package token

import (
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// DefaultContextThresholdRatio is the ratio of context window usage that triggers compression.
// When message tokens exceed MaxContextTokens * threshold, old messages are trimmed.
const DefaultContextThresholdRatio = 0.8

// CompressContext trims older history messages to bring total token count below
// the threshold.  It preserves:
//   - The system prompt (first message)
//   - The current turn: user query (last user message) and all subsequent
//     assistant/tool messages
//   - tool_call / tool_result message pairs (never splits them)
//
// currentTokens is the caller's best estimate of the current context size.
func CompressContext(
	messages []chat.Message,
	estimator *Estimator,
	maxTokens int,
	currentTokens int,
) []chat.Message {
	if maxTokens <= 0 || len(messages) <= 2 {
		return messages
	}

	threshold := int(float64(maxTokens) * DefaultContextThresholdRatio)
	if currentTokens <= threshold {
		return messages
	}

	systemMsg := messages[0]

	// Find the current user query — the last message with role "user".
	lastUserIdx := len(messages) - 1
	for i := len(messages) - 1; i >= 1; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	history := messages[1:lastUserIdx]
	tail := messages[lastUserIdx:]

	if len(history) == 0 {
		return messages
	}

	groups := groupToolMessages(history)

	tokensToFree := currentTokens - threshold
	freed := 0
	removeUpTo := 0

	for i, group := range groups {
		groupTokens := 0
		for _, msg := range group {
			groupTokens += estimator.EstimateMessage(&msg)
		}
		freed += groupTokens
		removeUpTo = i + 1
		if freed >= tokensToFree {
			break
		}
	}

	remaining := make([]chat.Message, 0, len(messages))
	remaining = append(remaining, systemMsg)
	for i := removeUpTo; i < len(groups); i++ {
		remaining = append(remaining, groups[i]...)
	}
	remaining = append(remaining, tail...)

	return remaining
}

// groupToolMessages groups middle messages into logical units:
//   - An assistant message with tool_calls + its corresponding tool result messages = one group
//   - A standalone message (user, assistant without tool_calls) = one group
//
// This ensures tool_call/tool_result pairs are never split during compression.
func groupToolMessages(messages []chat.Message) [][]chat.Message {
	var groups [][]chat.Message
	i := 0
	for i < len(messages) {
		msg := messages[i]

		// If this is an assistant message with tool_calls, group it with following tool results
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			group := []chat.Message{msg}
			i++
			// Collect all following tool result messages
			for i < len(messages) && messages[i].Role == "tool" {
				group = append(group, messages[i])
				i++
			}
			groups = append(groups, group)
		} else {
			groups = append(groups, []chat.Message{msg})
			i++
		}
	}
	return groups
}
