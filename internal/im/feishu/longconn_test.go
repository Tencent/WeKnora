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

	if got := convertEvent(RegionFeishu, "ou_bot", event); got != nil {
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

	got := convertEvent(RegionFeishu, "ou_bot", event)
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

func TestConvertEventIgnoresGroupMessageThatMentionsDifferentBot(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: stringPtr("text"),
				ChatType:    stringPtr("group"),
				ChatId:      stringPtr("oc_group"),
				MessageId:   stringPtr("om_message"),
				Content:     stringPtr(`{"text":"@_user_1 hello"}`),
				Mentions: []*larkim.MentionEvent{{
					Key:           stringPtr("@_user_1"),
					MentionedType: stringPtr("bot"),
					Name:          stringPtr("OtherBot"),
					Id:            &larkim.UserId{OpenId: stringPtr("ou_other_bot")},
				}},
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: stringPtr("ou_sender")},
			},
		},
	}

	if got := convertEvent(RegionFeishu, "ou_bot", event); got != nil {
		t.Fatalf("convertEvent() = %+v, want nil for group message mentioning a different bot", got)
	}
}

func TestConvertEventResolvesMentions(t *testing.T) {
	tests := []struct {
		name     string
		chatType string
		msgType  string
		content  string
		mentions []*larkim.MentionEvent
		want     string
	}{
		{
			name:     "direct text names the mentioned person",
			chatType: "p2p", msgType: "text",
			content: `{"text":"@_user_1 能获取到信息吗？"}`,
			mentions: []*larkim.MentionEvent{{
				Key: stringPtr("@_user_1"), Name: stringPtr("李朝盛"),
				Id: &larkim.UserId{OpenId: stringPtr("ou_person")},
			}},
			want: "@李朝盛 能获取到信息吗？",
		},
		{
			name:     "group text retains a person before the bot",
			chatType: "group", msgType: "text",
			content: `{"text":"@_user_1 @_user_2 请查询"}`,
			mentions: []*larkim.MentionEvent{
				{Key: stringPtr("@_user_1"), Name: stringPtr("王五"), Id: &larkim.UserId{OpenId: stringPtr("ou_person")}},
				{
					Key: stringPtr("@_user_2"), Name: stringPtr("WeKnora"), MentionedType: stringPtr("bot"),
					Id: &larkim.UserId{OpenId: stringPtr("ou_bot")},
				},
			},
			want: "@王五 请查询",
		},
		{
			name:     "post retains people and everyone but removes this bot",
			chatType: "group", msgType: "post",
			content: `{"content":[[{"tag":"at","user_id":"ou_person"},{"tag":"text","text":" 请查询 "},` +
				`{"tag":"at","user_id":"ou_bot"},{"tag":"at","user_id":"all"}]]}`,
			mentions: []*larkim.MentionEvent{
				{Key: stringPtr("@_user_1"), Name: stringPtr("王五"), Id: &larkim.UserId{OpenId: stringPtr("ou_person")}},
				{
					Key: stringPtr("@_user_2"), Name: stringPtr("WeKnora"), MentionedType: stringPtr("bot"),
					Id: &larkim.UserId{OpenId: stringPtr("ou_bot")},
				},
			},
			want: "@王五 请查询 @所有人",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageType: &tt.msgType, ChatType: &tt.chatType, ChatId: stringPtr("oc_group"),
					MessageId: stringPtr("om_message"), Content: &tt.content, Mentions: tt.mentions,
				},
			}}
			got := convertEvent(RegionFeishu, "ou_bot", event)
			if got == nil || got.Content != tt.want {
				t.Fatalf("convertEvent() = %+v, want content %q", got, tt.want)
			}
		})
	}
}

func TestConvertPostEventPreservesEmbeddedImage(t *testing.T) {
	content := `{"title":"","content":[[{"tag":"at","user_id":"ou_bot"},` +
		`{"tag":"text","text":"请描述这张图"},{"tag":"img","image_key":"img_v3_abc"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message", "ou_bot")
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

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message", "ou_bot")
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

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message", "ou_bot")
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
