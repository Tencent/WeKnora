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
		if msg.Role == "assistant" {
			msg.Content = withoutProtocolArtifactLines(msg.Content)
		}
		// Skip empty non-system messages (some providers reject these)
		if msg.Content == "" && msg.Role != "system" && msg.Role != "tool" && len(msg.ToolCalls) == 0 &&
			len(msg.Images) == 0 && len(msg.MultiContent) == 0 {
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
				// Copy instead of appending in place: prev may share its
				// backing arrays with the caller's slice. Images and content
				// parts go along for the same reason as tool calls: dropping
				// them leaves the model reading captions of images it never got.
				merged.ToolCalls = concatCopy(prev.ToolCalls, msg.ToolCalls)
				merged.Images = concatCopy(prev.Images, msg.Images)
				merged.MultiContent = concatCopy(prev.MultiContent, msg.MultiContent)
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

func concatCopy[T any](a, b []T) []T {
	if len(b) == 0 {
		return a
	}
	out := make([]T, 0, len(a)+len(b))
	return append(append(out, a...), b...)
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
