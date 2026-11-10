package wecom

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
)

// extractQuoteContent extracts text content from a quoted botMessage.
// Only returns actual textual content. Non-text types (image, file, video, etc.)
// return empty string so that buildQuotedMessage discards them — injecting
// "cannot see image" placeholders causes LLM hallucination from conversation history.
func extractQuoteContent(quote *botMessage) string {
	if quote == nil {
		return ""
	}
	switch quote.MsgType {
	case "text":
		return quote.Text.Content
	case "voice":
		return quote.Voice.Content // STT result; empty if recognition failed
	case "mixed":
		// Only keep text parts; skip images/other non-text items entirely.
		var parts []string
		for _, item := range quote.Mixed.MsgItem {
			if item.MsgType == "text" && item.Text.Content != "" {
				parts = append(parts, item.Text.Content)
			}
		}
		return strings.Join(parts, "\n")
	default:
		// image, file, video, unknown — no textual content available, discard.
		return ""
	}
}

// isQuoteFromBot determines whether a quoted message was sent by the bot.
// Uses two comparison strategies since WeCom's field mapping is undocumented.
func isQuoteFromBot(quote *botMessage, aiBotID string) bool {
	if quote == nil {
		return false
	}
	if quote.From.UserID != "" && aiBotID != "" && quote.From.UserID == aiBotID {
		return true
	}
	if quote.AiBotID != "" && quote.AiBotID == aiBotID {
		return true
	}
	return false
}

// buildQuotedMessage constructs a QuotedMessage from a botMessage quote.
// Returns nil only if quote itself is nil.
// For non-text quotes (image, file, video), Content is empty and NonTextType
// is set so downstream can generate an LLM instruction instead of a placeholder.
func buildQuotedMessage(quote *botMessage, aiBotID string) *im.QuotedMessage {
	if quote == nil {
		return nil
	}
	content := extractQuoteContent(quote)
	result := &im.QuotedMessage{
		MessageID:    quote.MsgID,
		Content:      content,
		SenderID:     quote.From.UserID,
		IsBotMessage: isQuoteFromBot(quote, aiBotID),
	}
	if content == "" {
		result.NonTextType = quote.MsgType
	}
	return result
}
