package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/gin-gonic/gin"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestFeishuInboundMaterialsMatchAcrossTransports(t *testing.T) {
	botID, otherID := "ou_bot", "ou_other"
	mentionKey := "@_user_1"
	for _, tc := range []struct {
		name, kind, content, chatType string
		mentionID                     string
		wantText                      string
		wantImages                    int
		wantAccepted                  bool
	}{
		{"text and bot mention", "text", `{"text":"@_user_1 请解释日志"}`, "group", botID, "请解释日志", 0, true},
		{"wrong mention", "text", `{"text":"@_user_1 请解释日志"}`, "group", otherID, "", 0, false},
		{"no mention", "text", `{"text":"请解释日志"}`, "group", "", "", 0, false},
		{"metadata alone is not a mention", "text", `{"text":"请解释日志"}`, "group", botID, "", 0, false},
		{"mention key prefix is not a match", "text", `{"text":"@_user_10 请解释日志"}`, "group", botID, "", 0, false},
		{"mention in forwarded content", "merge_forward", `{"text":"@_user_1"}`, "group", botID, "", 0, false},
		{"private forward", "merge_forward", `{}`, "p2p", "", "", 0, true},
		{"private image", "image", `{"image_key":"img_only"}`, "p2p", "", "", 1, true},
		{
			"image post", "post",
			`{"title":"","content":[[{"tag":"at","user_id":"ou_bot"},
				{"tag":"img","image_key":"img_only"}]]}`,
			"group", botID, "", 1, true,
		},
		{
			"two images", "post",
			`{"title":"Comparison","content":[[{"tag":"at","user_id":"ou_bot"},
				{"tag":"text","text":"A"},
				{"tag":"img","image_key":"img_a"},
				{"tag":"text","text":"B"},
				{"tag":"img","image_key":"img_b"}]]}`,
			"group", botID, "Comparison\nAB", 2, true,
		},
		{
			"text post", "post",
			`{"title":"Status","content":[[{"tag":"text","text":"all systems go"}]]}`,
			"p2p", "", "Status\nall systems go", 0, true,
		},
		{
			"localized post", "post",
			`{"en_us":{"title":"Compare","content_v2":[[{"tag":"img","image_key":"img_a"},
				{"tag":"text","text":"then"},
				{"tag":"img","image_key":"img_b"}]]}}`,
			"p2p", "", "Compare\nthen", 2, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &Adapter{region: RegionFeishu, botOpenID: botID}
			var mentions []*larkim.MentionEvent
			if tc.mentionID != "" {
				mentions = []*larkim.MentionEvent{{Key: &mentionKey, Id: &larkim.UserId{OpenId: &tc.mentionID}}}
			}
			messageID, parentID, rootID := "om_current", "om_parent", "om_root"
			chatID, senderID := "oc_chat", "ou_sender"
			event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &messageID, ParentId: &parentID, RootId: &rootID,
					ChatId: &chatID, ChatType: &tc.chatType, MessageType: &tc.kind,
					Content: &tc.content, Mentions: mentions,
				},
				Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: &senderID}},
			}}
			ws, err := adapter.convertEvent(context.Background(), event)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(feishuEventBody{
				Header: &feishuEventHeader{EventType: "im.message.receive_v1"},
				Event: &feishuEvent{
					Message: &feishuMessage{
						MessageID: messageID, ParentID: parentID, RootID: rootID, ChatID: chatID,
						ChatType: tc.chatType, MessageType: tc.kind, Content: tc.content, Mentions: mentions,
					},
					Sender: &feishuSender{SenderID: &feishuSenderID{OpenID: senderID}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/callback", bytes.NewReader(body))
			webhook, err := adapter.ParseCallback(c)
			if err != nil {
				t.Fatal(err)
			}
			if (ws != nil) != tc.wantAccepted || (webhook != nil) != tc.wantAccepted {
				t.Fatalf("accepted: ws=%v webhook=%v, want %v", ws != nil, webhook != nil, tc.wantAccepted)
			}
			if !tc.wantAccepted {
				return
			}
			if webhook.ThreadID != rootID || ws.ThreadID != "" {
				t.Fatal("existing thread-session behavior changed")
			}
			webhook.ThreadID = ""
			if !reflect.DeepEqual(ws, webhook) {
				t.Fatalf("transports differ: ws=%+v webhook=%+v", ws, webhook)
			}
			if ws.Content != tc.wantText || ws.Material.ParentID != parentID {
				t.Fatalf("text=%q parent=%q", ws.Content, ws.Material.ParentID)
			}
			images := 0
			for _, part := range ws.Material.Parts {
				if part.Type == im.MessageTypeImage {
					images++
				}
			}
			if images != tc.wantImages {
				t.Fatalf("images=%d, want %d", images, tc.wantImages)
			}
			if tc.name == "two images" {
				parts := ws.Material.Parts
				if len(parts) != 6 ||
					parts[1].Text != "A" ||
					parts[2].FileKey != "img_a" ||
					parts[3].Text != "B" ||
					parts[4].FileKey != "img_b" {
					t.Fatalf("lost relative text/image order: %+v", parts)
				}
			}
		})
	}
}
