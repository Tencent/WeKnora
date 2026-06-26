package qqbot

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

func TestParseGatewayPayloadC2CMessage(t *testing.T) {
	event := messageEvent{
		ID:      "msg-1",
		Content: "  hello  ",
		Author:  qqbotAuthor{UserOpenID: "user-openid", Username: "tester"},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := parseGatewayPayload(&gatewayPayload{Op: opDispatch, T: eventC2CMessageCreate, D: raw})
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Platform != im.PlatformQQBot || msg.ChatType != im.ChatTypeDirect || msg.UserID != "user-openid" || msg.Content != "hello" {
		t.Fatalf("unexpected message: %#v", msg)
	}
	if msg.Extra[extraKeyMessageID] != "msg-1" || msg.Extra[extraKeyChatKind] != "c2c" {
		t.Fatalf("unexpected extra: %#v", msg.Extra)
	}
}

func TestParseGatewayPayloadGroupMessage(t *testing.T) {
	event := messageEvent{
		ID:          "msg-2",
		Content:     " group hello ",
		GroupOpenID: "group-openid",
		Author:      qqbotAuthor{MemberOpenID: "member-openid"},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := parseGatewayPayload(&gatewayPayload{Op: opDispatch, T: eventGroupAtMessageCreate, D: raw})
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.ChatType != im.ChatTypeGroup || msg.ChatID != "group-openid" || msg.UserID != "member-openid" || msg.Content != "group hello" {
		t.Fatalf("unexpected message: %#v", msg)
	}
}

func TestParseExpiresIn(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "number", raw: `3600`, want: 3600},
		{name: "string", raw: `"1800"`, want: 1800},
		{name: "empty", raw: ``, want: 7200},
		{name: "invalid", raw: `"bad"`, want: 7200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseExpiresIn(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("parseExpiresIn() = %d, want %d", got, tc.want)
			}
		})
	}
}
