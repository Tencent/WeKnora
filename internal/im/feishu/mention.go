package feishu

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// resolveFeishuMentions replaces @_user_N placeholders with readable mentions (@name or @所有人)
// and in group chat strips mentions of the bot itself.
func resolveFeishuMentions(content string, mentions []*larkim.MentionEvent, chatType im.ChatType, botOpenID string) string {
	if len(mentions) == 0 {
		if chatType == im.ChatTypeGroup {
			for strings.HasPrefix(content, "@_user_") {
				idx := strings.Index(content, " ")
				if idx >= 0 {
					content = content[idx+1:]
				} else {
					break
				}
			}
		}
		return strings.TrimSpace(content)
	}

	for _, m := range mentions {
		if m == nil || m.Key == nil {
			continue
		}
		key := *m.Key
		if key == "" {
			continue
		}

		isThisBot := false
		if m.MentionedType != nil && *m.MentionedType == "bot" {
			if m.Id != nil && m.Id.OpenId != nil && *m.Id.OpenId == botOpenID {
				isThisBot = true
			}
		}

		if chatType == im.ChatTypeGroup && isThisBot {
			content = strings.ReplaceAll(content, key+" ", "")
			content = strings.ReplaceAll(content, key, "")
			continue
		}

		name := ""
		if m.Name != nil && *m.Name != "" {
			name = *m.Name
		} else if m.MentionedType != nil && *m.MentionedType == "all" {
			name = "所有人"
		}
		if name != "" {
			content = strings.ReplaceAll(content, key, "@"+name)
		}
	}

	return strings.TrimSpace(content)
}
