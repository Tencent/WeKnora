package feishu

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func stringPtr(s string) *string { return &s }

func TestConvertEventIgnoresGroupMessageWithoutBotMention(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: stringPtr("text"),
				ChatType:    stringPtr("group"),
				ChatId:      stringPtr("oc_group"),
				MessageId:   stringPtr("om_message"),
				Content:     stringPtr(`{"text":"知识库最新文档"}`),
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: stringPtr("ou_sender")},
			},
		},
	}

	if got := convertEvent(RegionFeishu, event); got != nil {
		t.Fatalf("convertEvent() = %+v, want nil for group message without bot mention", got)
	}
}

func TestConvertEventKeepsGroupMessageThatMentionsBot(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: stringPtr("text"),
				ChatType:    stringPtr("group"),
				ChatId:      stringPtr("oc_group"),
				MessageId:   stringPtr("om_message"),
				Content:     stringPtr(`{"text":"@_user_1 亚信有什么ai安全产品"}`),
				Mentions: []*larkim.MentionEvent{{
					Key:           stringPtr("@_user_1"),
					MentionedType: stringPtr("bot"),
					Name:          stringPtr("WeKnora"),
					Id:            &larkim.UserId{OpenId: stringPtr("ou_bot")},
				}},
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: stringPtr("ou_sender")},
			},
		},
	}

	got := convertEvent(RegionFeishu, event)
	if got == nil {
		t.Fatal("convertEvent() returned nil for group message that mentions the bot")
	}
	if got.Content != "亚信有什么ai安全产品" {
		t.Fatalf("Content = %q, want %q", got.Content, "亚信有什么ai安全产品")
	}
	if got.ChatType != im.ChatTypeGroup {
		t.Fatalf("ChatType = %q, want %q", got.ChatType, im.ChatTypeGroup)
	}
}

func TestConvertPostEventPreservesEmbeddedImage(t *testing.T) {
	content := `{"title":"","content":[[{"tag":"at","user_id":"ou_bot"},{"tag":"text","text":"请描述这张图"},{"tag":"img","image_key":"img_v3_abc"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if got.MessageType != im.MessageTypeImage {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
	if got.Content != "请描述这张图" {
		t.Fatalf("Content = %q, want %q", got.Content, "请描述这张图")
	}
	if got.FileKey != "img_v3_abc" {
		t.Fatalf("FileKey = %q, want %q", got.FileKey, "img_v3_abc")
	}
	if got.FileName != "img_v3_abc.png" {
		t.Fatalf("FileName = %q, want %q", got.FileName, "img_v3_abc.png")
	}
}

func TestConvertPostEventPreservesImageWithoutText(t *testing.T) {
	content := `{"title":"","content":[[{"tag":"at","user_id":"ou_bot"},{"tag":"img","image_key":"img_v3_only"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if got.MessageType != im.MessageTypeImage {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
	if got.Content != "" {
		t.Fatalf("Content = %q, want empty", got.Content)
	}
	if got.FileKey != "img_v3_only" {
		t.Fatalf("FileKey = %q, want %q", got.FileKey, "img_v3_only")
	}
}

func TestConvertPostEventKeepsTextOnlyPostsAsText(t *testing.T) {
	content := `{"title":"Status","content":[[{"tag":"text","text":"all systems go"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if got.MessageType != im.MessageTypeText {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeText)
	}
	if got.Content != "Status\nall systems go" {
		t.Fatalf("Content = %q, want %q", got.Content, "Status\\nall systems go")
	}
	if got.FileKey != "" {
		t.Fatalf("FileKey = %q, want empty", got.FileKey)
	}
}
