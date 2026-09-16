package wecom

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// mixedItem builds one botMixedItem element for tests.
func mixedTextItem(content string) botMixedItem {
	item := botMixedItem{MsgType: "text"}
	item.Text.Content = content
	return item
}

func mixedImageItem(url, aesKey string) botMixedItem {
	item := botMixedItem{MsgType: "image"}
	item.Image.URL = url
	item.Image.AESKey = aesKey
	return item
}

func TestConvertMixedMessage(t *testing.T) {
	tests := []struct {
		name        string
		chatType    im.ChatType
		items       []botMixedItem
		wantNil     bool
		wantType    im.MessageType
		wantContent string
		wantFileKey string
		wantAESKey  string
	}{
		{
			name:        "text only",
			chatType:    im.ChatTypeDirect,
			items:       []botMixedItem{mixedTextItem("hello")},
			wantType:    im.MessageTypeText,
			wantContent: "hello",
		},
		{
			name:        "image only",
			chatType:    im.ChatTypeDirect,
			items:       []botMixedItem{mixedImageItem("https://img.example/a.png", "key-a")},
			wantType:    im.MessageTypeImage,
			wantFileKey: "https://img.example/a.png",
			wantAESKey:  "key-a",
		},
		{
			// The common real-world case: the user attaches an image AND types a
			// question in the same turn. Both must survive — the text becomes the
			// QA query and the image is passed to the model as vision input.
			name:        "text and image keeps both",
			chatType:    im.ChatTypeDirect,
			items:       []botMixedItem{mixedImageItem("https://img.example/b.png", "key-b"), mixedTextItem("这个是什么产品的说明")},
			wantType:    im.MessageTypeImage,
			wantContent: "这个是什么产品的说明",
			wantFileKey: "https://img.example/b.png",
			wantAESKey:  "key-b",
		},
		{
			name:        "group message strips at-mention and keeps image",
			chatType:    im.ChatTypeGroup,
			items:       []botMixedItem{mixedTextItem("@小电厨电助手 "), mixedImageItem("https://img.example/c.png", "key-c"), mixedTextItem("这个是什么产品的说明")},
			wantType:    im.MessageTypeImage,
			wantContent: "这个是什么产品的说明",
			wantFileKey: "https://img.example/c.png",
			wantAESKey:  "key-c",
		},
		{
			name:     "empty mixed returns nil",
			chatType: im.ChatTypeDirect,
			items:    []botMixedItem{mixedTextItem("   ")},
			wantNil:  true,
		},
	}

	client := &LongConnClient{}
	// Group-chat @-mention stripping matches the bot name the client learns
	// from earlier messages (Strategy 2 in stripAtMention); seed it so the
	// group case is deterministic.
	client.botDisplayName.Store("小电厨电助手")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &botMessage{
				MsgID:    "msg-1",
				ChatType: "single",
				MsgType:  "mixed",
				Mixed: struct {
					MsgItem []botMixedItem `json:"msg_item"`
				}{MsgItem: tt.items},
			}

			got := client.convertMixedMessage(msg, "chat-1", tt.chatType, "req-1")

			if tt.wantNil {
				if got != nil {
					t.Fatalf("convertMixedMessage() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("convertMixedMessage() = nil, want message")
			}
			if got.MessageType != tt.wantType {
				t.Errorf("MessageType = %q, want %q", got.MessageType, tt.wantType)
			}
			if got.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", got.Content, tt.wantContent)
			}
			if got.FileKey != tt.wantFileKey {
				t.Errorf("FileKey = %q, want %q", got.FileKey, tt.wantFileKey)
			}
			if tt.wantAESKey != "" && got.Extra["aes_key"] != tt.wantAESKey {
				t.Errorf("Extra[aes_key] = %q, want %q", got.Extra["aes_key"], tt.wantAESKey)
			}
		})
	}
}
