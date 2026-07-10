package wecom_chat_archive

import (
	"strings"
	"testing"
)

func TestParseChatDataResponseReadsChatData(t *testing.T) {
	raw := []byte(`{"errcode":0,"errmsg":"ok","chatdata":[{"seq":196,"msgid":"m1","publickey_ver":3,"encrypt_random_key":"k","encrypt_chat_msg":"c"}]}`)
	items, hasMore, err := parseChatDataResponse(raw)
	if err != nil {
		t.Fatalf("parseChatDataResponse error: %v", err)
	}
	if len(items) != 1 || items[0].Seq != 196 || items[0].MsgID != "m1" {
		t.Fatalf("items = %#v", items)
	}
	if hasMore {
		t.Fatal("hasMore should be false when returned item count is below limit in caller")
	}
}

func TestParseChatDataResponseReturnsSDKError(t *testing.T) {
	raw := []byte(`{"errcode":10009,"errmsg":"ip invalid"}`)
	_, _, err := parseChatDataResponse(raw)
	if err == nil || !strings.Contains(err.Error(), "10009") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeTextMessage(t *testing.T) {
	payload := []byte(`{"msgid":"m1","action":"send","from":"zhangsan","tolist":["lisi"],"roomid":"wr_xxx","msgtime":1783405282000,"msgtype":"text","text":{"content":"hello"}}`)
	msg, err := decodeDecryptedMessage(196, "m1", payload)
	if err != nil {
		t.Fatalf("decodeDecryptedMessage error: %v", err)
	}
	if msg.Seq != 196 || msg.MsgID != "m1" || msg.MsgType != "text" {
		t.Fatalf("msg = %#v", msg)
	}
	if msg.ConversationID != "wr_xxx" || msg.ConversationType != conversationTypeRoom {
		t.Fatalf("conversation = %q/%q", msg.ConversationID, msg.ConversationType)
	}
	if msg.From.UserID != "zhangsan" || msg.ToList[0].UserID != "lisi" {
		t.Fatalf("senders = %#v -> %#v", msg.From, msg.ToList)
	}
	if string(msg.Raw) != "hello" {
		t.Fatalf("Raw = %q, want text body", string(msg.Raw))
	}
}

func TestDecodeExternalSingleConversation(t *testing.T) {
	payload := []byte(`{"msgid":"m2","action":"send","from":"wm_ext","tolist":["zhangsan"],"msgtime":1783405282000,"msgtype":"text","text":{"content":"external hello"}}`)
	msg, err := decodeDecryptedMessage(197, "m2", payload)
	if err != nil {
		t.Fatalf("decodeDecryptedMessage error: %v", err)
	}
	if msg.ConversationType != conversationTypeSingle {
		t.Fatalf("ConversationType = %q", msg.ConversationType)
	}
	if msg.ConversationID == "" {
		t.Fatal("ConversationID should be stable for single chat")
	}
	if msg.From.ExternalUserID != "wm_ext" {
		t.Fatalf("From = %#v", msg.From)
	}
}
