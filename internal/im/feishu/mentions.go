package feishu

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var mentionPlaceholder = regexp.MustCompile(`@_user_[0-9]+ ?`)

// renderTextMentions uses the event's mention metadata instead of sending
// Feishu's opaque placeholders to the agent. Only this app's own mention is
// removed from group messages; mentions of people and other bots are retained.
func renderTextMentions(
	content string, mentions []*larkim.MentionEvent, botOpenID string, chatType im.ChatType,
) string {
	if len(mentions) == 0 {
		if chatType == im.ChatTypeGroup {
			return stripLeadingMentionPlaceholders(content)
		}
		return content
	}

	byKey := make(map[string]*larkim.MentionEvent, len(mentions))
	for _, mention := range mentions {
		if mention != nil && ptrStr(mention.Key) != "" {
			byKey[ptrStr(mention.Key)] = mention
		}
	}
	return mentionPlaceholder.ReplaceAllStringFunc(content, func(raw string) string {
		key := strings.TrimSuffix(raw, " ")
		mention := byKey[key]
		if mention == nil {
			return raw
		}
		if isOwnBotMention(mention, botOpenID, chatType) {
			return ""
		}
		name := ptrStr(mention.Name)
		if name == "" {
			name = "用户"
			if ptrStr(mention.MentionedType) == "bot" {
				name = "机器人"
			}
		}
		return "@" + name + strings.TrimPrefix(raw, key)
	})
}

func renderPostMention(userID string, mentions []*larkim.MentionEvent, botOpenID string, chatType im.ChatType) string {
	if userID == "all" {
		return "@所有人"
	}
	if len(mentions) == 0 {
		return ""
	}
	for _, mention := range mentions {
		if mention == nil || mention.Id == nil || ptrStr(mention.Id.OpenId) != userID {
			continue
		}
		if isOwnBotMention(mention, botOpenID, chatType) {
			return ""
		}
		if name := ptrStr(mention.Name); name != "" {
			return "@" + name
		}
		return "@用户"
	}
	return "@用户"
}

func isOwnBotMention(mention *larkim.MentionEvent, botOpenID string, chatType im.ChatType) bool {
	return chatType == im.ChatTypeGroup && botOpenID != "" && mention.Id != nil &&
		ptrStr(mention.MentionedType) == "bot" && ptrStr(mention.Id.OpenId) == botOpenID
}

func stripLeadingMentionPlaceholders(content string) string {
	for strings.HasPrefix(content, "@_user_") {
		idx := strings.IndexByte(content, ' ')
		if idx < 0 {
			break
		}
		content = content[idx+1:]
	}
	return content
}
