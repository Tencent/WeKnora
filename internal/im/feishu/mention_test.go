package feishu

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func strPtr(s string) *string {
	return &s
}

func TestResolveFeishuMentions_DirectChat(t *testing.T) {
	mentions := []*larkim.MentionEvent{
		{
			Key:           strPtr("@_user_1"),
			Name:          strPtr("李朝盛"),
			MentionedType: strPtr("user"),
			Id: &larkim.UserId{
				OpenId: strPtr("ou_user1"),
			},
		},
	}

	content := "@_user_1 能获取到@人的信息吗？"
	got := resolveFeishuMentions(content, mentions, im.ChatTypeDirect, "ou_bot")
	want := "@李朝盛 能获取到@人的信息吗？"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveFeishuMentions_GroupChatWithBotAndUser(t *testing.T) {
	mentions := []*larkim.MentionEvent{
		{
			Key:           strPtr("@_user_1"),
			Name:          strPtr("王五"),
			MentionedType: strPtr("user"),
			Id: &larkim.UserId{
				OpenId: strPtr("ou_user1"),
			},
		},
		{
			Key:           strPtr("@_user_2"),
			Name:          strPtr("智能助手"),
			MentionedType: strPtr("bot"),
			Id: &larkim.UserId{
				OpenId: strPtr("ou_bot"),
			},
		},
	}

	// In group chat, bot mention should be stripped, real user mention should be preserved
	content := "@_user_1 @_user_2 查一下"
	got := resolveFeishuMentions(content, mentions, im.ChatTypeGroup, "ou_bot")
	want := "@王五 查一下"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveFeishuMentions_MentionAll(t *testing.T) {
	mentions := []*larkim.MentionEvent{
		{
			Key:           strPtr("@_user_1"),
			MentionedType: strPtr("all"),
		},
	}

	content := "@_user_1 请大家注意查收"
	got := resolveFeishuMentions(content, mentions, im.ChatTypeGroup, "ou_bot")
	want := "@所有人 请大家注意查收"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveFeishuMentions_FallbackWithoutMentions(t *testing.T) {
	content := "@_user_1 @_user_2 查一下"
	got := resolveFeishuMentions(content, nil, im.ChatTypeGroup, "ou_bot")
	want := "查一下"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
