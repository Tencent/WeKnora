package types

import "strings"

// DefaultFeishuReplyTitle preserves the identity used by existing agents.
const DefaultFeishuReplyTitle = "WeKnora"

// FeishuCardTitle distinguishes legacy configuration from an explicit choice
// to follow the agent name, without rewriting stored agent configurations.
func (a *CustomAgent) FeishuCardTitle() string {
	if a == nil || a.Config.FeishuReplyTitle == nil {
		return DefaultFeishuReplyTitle
	}
	title := strings.TrimSpace(*a.Config.FeishuReplyTitle)
	if title == "" {
		title = strings.TrimSpace(a.Name)
	}
	if title == "" {
		return DefaultFeishuReplyTitle
	}
	runes := []rune(title)
	if len(runes) > 100 {
		title = string(runes[:100])
	}
	return title
}
