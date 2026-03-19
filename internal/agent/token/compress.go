package token

import (
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// DefaultContextThresholdRatio is the ratio of context window usage that triggers compression.
// When message tokens exceed MaxContextTokens * threshold, old messages are trimmed.
const DefaultContextThresholdRatio = 0.8

// CompressContext trims older messages to bring total token count below the threshold.
// It preserves:
//   - The first message (system prompt)
//   - The last message (current user query)
//   - tool_call / tool_result message pairs (never splits them)
//
// Returns the compressed message slice. If no compression is needed, returns the original.
func CompressContext(
	messages []chat.Message,
	estimator *Estimator,
	maxTokens int,
) []chat.Message {
	if maxTokens <= 0 || len(messages) <= 2 {
		return messages
	}

	threshold := int(float64(maxTokens) * DefaultContextThresholdRatio)
	currentTokens := estimator.EstimateMessages(messages)
	if currentTokens <= threshold {
		return messages
	}

	// Strategy: keep system prompt (first) + current query (last),
	// trim from the oldest messages inward, respecting tool_call/tool_result boundaries.

	systemMsg := messages[0]
	lastMsg := messages[len(messages)-1]
	middle := messages[1 : len(messages)-1]

	// Find groups of messages that form tool_call/tool_result pairs.
	// A group = one assistant message with tool_calls + all following tool result messages.
	groups := groupToolMessages(middle)

	// Remove groups from the front until we're under the threshold.
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

	// Rebuild messages: system + remaining groups + current query
	remaining := make([]chat.Message, 0, len(messages))
	remaining = append(remaining, systemMsg)
	for i := removeUpTo; i < len(groups); i++ {
		remaining = append(remaining, groups[i]...)
	}
	remaining = append(remaining, lastMsg)

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
