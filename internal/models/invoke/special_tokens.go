package invoke

import "strings"

// Ported from upstream main (internal/models/chat/special_tokens.go, merge
// f2606629): message text is data, never chat-template syntax. Self-hosted tokenizers can
// recognize these literals even inside tool results or quoted documents. Break
// the literal with a zero-width space only in the outbound copy; actual roles
// continue to be serialized as structured provider fields.
var specialTokenLiterals = strings.NewReplacer(
	"<|", "<\u200b|",
	"<｜", "<\u200b｜",
	"<start_of_turn>", "<\u200bstart_of_turn>",
	"<end_of_turn>", "<\u200bend_of_turn>",
	"[INST]", "[\u200bINST]",
	"[/INST]", "[\u200b/INST]",
	"<<SYS>>", "<\u200b<SYS>>",
	"<</SYS>>", "<\u200b</SYS>>",
)

// The neutral model keeps text in Parts (design §6.1) instead of v1's string
// Content + MultiContent; the hook now runs once at the entry (chatExecute)
// rather than per-provider converter, so every adapter inherits it.
func neutralizeMessageSpecialTokens(message Message) Message {
	if len(message.Content) != 0 {
		message.Content = append([]Part(nil), message.Content...)
		for i := range message.Content {
			if message.Content[i].Text != "" {
				message.Content[i].Text = specialTokenLiterals.Replace(message.Content[i].Text)
			}
		}
	}
	message.ReasoningContent = specialTokenLiterals.Replace(message.ReasoningContent)
	if len(message.ToolCalls) != 0 {
		message.ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
		for i := range message.ToolCalls {
			arguments := message.ToolCalls[i].Function.Arguments
			message.ToolCalls[i].Function.Arguments = specialTokenLiterals.Replace(arguments)
		}
	}
	return message
}
