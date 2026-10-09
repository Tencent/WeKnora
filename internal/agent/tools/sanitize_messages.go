package tools

import (
	"html"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

// SanitizeMessages validates and fixes a message array for LLM compatibility.
// It handles common issues that cause provider API errors:
//   - Ensures no consecutive same-role messages (some providers reject these)
//   - Verifies tool result messages have matching tool_call in the preceding assistant message
//   - Removes empty content messages that can cause API errors
//
// Returns the sanitized message slice (may be shorter than input).
func SanitizeMessages(messages []chat.Message) []chat.Message {
	if len(messages) == 0 {
		return messages
	}

	result := make([]chat.Message, 0, len(messages))
	for i, msg := range messages {
		// Skip empty non-system messages (some providers reject these)
		if msg.Content == "" && msg.Role != "system" &&
			msg.Role != "tool" && len(msg.ToolCalls) == 0 {
			continue
		}

		// Prevent consecutive same-role messages (except tool results)
		if len(result) > 0 && msg.Role != "tool" {
			prev := result[len(result)-1]
			if prev.Role == msg.Role && prev.Role != "tool" {
				// Merge with previous message.
				//
				// The merge has to take the tool calls along: the tool results
				// that follow still reference them, and a tool message whose
				// tool_call_id has no matching assistant call is rejected by
				// OpenAI-compatible endpoints. Dropping them here is what used
				// to turn one merged pair into a 400, and the agent then fell
				// back to a tool-less summary call where the model can only
				// write the call it wanted to make as text.
				merged := prev
				merged.Content += "\n\n" + msg.Content
				if len(msg.ToolCalls) > 0 {
					// Copy instead of appending in place: prev may share its
					// backing array with the caller's slice.
					calls := make([]chat.ToolCall, 0, len(prev.ToolCalls)+len(msg.ToolCalls))
					calls = append(calls, prev.ToolCalls...)
					calls = append(calls, msg.ToolCalls...)
					merged.ToolCalls = calls
				}
				result[len(result)-1] = merged
				continue
			}
		}

		// Verify tool result messages reference a valid tool call
		if msg.Role == "tool" && msg.ToolCallID != "" {
			if !hasMatchingToolCall(messages[:i], msg.ToolCallID) {
				// Preserve recoverable data without promoting external output to policy.
				msg.Role = "user"
				msg.Content = "<untrusted_tool_result name=\"" + html.EscapeString(msg.Name) +
					"\">\n" + html.EscapeString(msg.Content) + "\n</untrusted_tool_result>"
				msg.ToolCallID = ""
				msg.Name = ""
			}
		}

		result = append(result, msg)
	}

	return result
}

// hasMatchingToolCall checks if any preceding assistant message has a tool call with the given ID.
func hasMatchingToolCall(messages []chat.Message, toolCallID string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				if tc.ID == toolCallID {
					return true
				}
			}
		}
	}
	return false
}
