package wecom

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
)

// extractQuoteContent extracts text content from a quoted botMessage.
// For non-text message types, returns a descriptive placeholder.
func extractQuoteContent(quote *botMessage) string {
	if quote == nil {
		return ""
	}
	switch quote.MsgType {
	case "text":
		return quote.Text.Content
	case "voice":
		if quote.Voice.Content != "" {
			return quote.Voice.Content
		}
		return "[语音消息]"
	case "mixed":
		var parts []string
		for _, item := range quote.Mixed.MsgItem {
			if item.MsgType == "text" && item.Text.Content != "" {
				parts = append(parts, item.Text.Content)
			} else if item.MsgType == "image" {
				parts = append(parts, "[图片]")
			}
		}
		return strings.Join(parts, "\n")
	case "image":
		return "[图片]"
	case "file":
		return "[文件]"
	case "video":
		return "[视频]"
	default:
		return "[消息]"
	}
}

// isQuoteFromBot determines whether a quoted message was sent by the bot.
// Uses two comparison strategies since WeCom's field mapping is undocumented.
func isQuoteFromBot(quote *botMessage, aiBotID string) bool {
	if quote.From.UserID != "" && aiBotID != "" && quote.From.UserID == aiBotID {
		return true
	}
	if quote.AiBotID != "" && quote.AiBotID == aiBotID {
		return true
	}
	return false
}

// buildQuotedMessage constructs a QuotedMessage from a botMessage quote.
// Returns nil if the quote is nil or has no extractable content.
func buildQuotedMessage(quote *botMessage, aiBotID string) *im.QuotedMessage {
	if quote == nil {
		return nil
	}
	content := extractQuoteContent(quote)
	if content == "" {
		return nil
	}
	return &im.QuotedMessage{
		MessageID:    quote.MsgID,
		Content:      content,
		SenderID:     quote.From.UserID,
		IsBotMessage: isQuoteFromBot(quote, aiBotID),
	}
}
