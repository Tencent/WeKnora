package tools

import (
	"html"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// SanitizeMessages validates and fixes a message array for LLM compatibility.
// It handles common issues that cause provider API errors:
//   - Ensures no consecutive same-role messages (some providers reject these)
//   - Verifies tool result messages have matching tool_call in the preceding assistant message
//   - Removes empty content messages that can cause API errors
//
// Returns the sanitized message slice (may be shorter than input).
func SanitizeMessages(messages []invoke.Message) []invoke.Message {
	if len(messages) == 0 {
		return messages
	}

	result := make([]invoke.Message, 0, len(messages))
	for i, msg := range messages {
		// Skip empty non-system messages (some providers reject these)
		if msg.Text() == "" && msg.Role != "system" &&
			msg.Role != "tool" && len(msg.ToolCalls) == 0 {
			continue
		}

		// Prevent consecutive same-role messages (except tool results)
		if len(result) > 0 && msg.Role != "tool" {
			prev := result[len(result)-1]
			if prev.Role == msg.Role && prev.Role != "tool" {
				// Merge with previous message
				result[len(result)-1].Content = append(result[len(result)-1].Content,
					invoke.Part{Text: "\n\n" + msg.Text()})
				continue
			}
		}

		// Verify tool result messages reference a valid tool call
		if msg.Role == "tool" && msg.ToolCallID != "" {
			if !hasMatchingToolCall(messages[:i], msg.ToolCallID) {
				// Preserve recoverable data without promoting external output
				// to policy: an orphaned tool result stays user-role content,
				// wrapped and escaped (upstream 7a98a8e3).
				msg.Role = invoke.RoleUser
				msg.Content = []invoke.Part{{Text: "<untrusted_tool_result name=\"" + html.EscapeString(msg.Name) +
					"\">\n" + html.EscapeString(msg.Text()) + "\n</untrusted_tool_result>"}}
				msg.ToolCallID = ""
				msg.Name = ""
			}
		}

		result = append(result, msg)
	}

	return result
}

// hasMatchingToolCall checks if any preceding assistant message has a tool call with the given ID.
func hasMatchingToolCall(messages []invoke.Message, toolCallID string) bool {
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
