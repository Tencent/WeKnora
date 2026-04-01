package im

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFormatQuotedContext(t *testing.T) {
	tests := []struct {
		name  string
		quote *QuotedMessage
		want  string
	}{
		{
			name:  "nil quote",
			quote: nil,
			want:  "",
		},
		{
			name:  "empty content no NonTextType",
			quote: &QuotedMessage{Content: ""},
			want:  "",
		},
		{
			name:  "non-text image quote generates instruction",
			quote: &QuotedMessage{NonTextType: "image"},
			want:  "用户引用了一条图片消息，但你无法查看该内容。请直接告知用户你目前无法处理图片消息，建议用户用文字描述问题。不要猜测该消息的内容。",
		},
		{
			name:  "non-text file quote generates instruction",
			quote: &QuotedMessage{NonTextType: "file"},
			want:  "用户引用了一条文件消息，但你无法查看该内容。请直接告知用户你目前无法处理文件消息，建议用户用文字描述问题。不要猜测该消息的内容。",
		},
		{
			name:  "non-text unknown type uses fallback label",
			quote: &QuotedMessage{NonTextType: "location"},
			want:  "用户引用了一条该类型的消息，但你无法查看该内容。请直接告知用户你目前无法处理该类型的消息，建议用户用文字描述问题。不要猜测该消息的内容。",
		},
		{
			name:  "bot message",
			quote: &QuotedMessage{Content: "bot reply text", IsBotMessage: true},
			want:  "以下是用户引用的你（机器人）之前的回复，仅作为上下文参考：\n<quoted_message>\nbot reply text\n</quoted_message>",
		},
		{
			name:  "user message",
			quote: &QuotedMessage{Content: "user message text", IsBotMessage: false},
			want:  "以下是用户引用的一条历史消息，仅作为上下文参考：\n<quoted_message>\nuser message text\n</quoted_message>",
		},
		{
			name: "truncation at 500 runes",
			quote: &QuotedMessage{
				Content:      string(make([]rune, 600)),
				IsBotMessage: false,
			},
			want: "以下是用户引用的一条历史消息，仅作为上下文参考：\n<quoted_message>\n" + string(make([]rune, 500)) + "...\n</quoted_message>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatQuotedContext(tt.quote)
			if got != tt.want {
				t.Errorf("formatQuotedContext() length = %d, want length %d", len(got), len(tt.want))
				if len(got) < 200 && len(tt.want) < 200 {
					t.Errorf("got = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestBuildIMQARequest_QuotedContext(t *testing.T) {
	session := &types.Session{ID: "s1"}

	t.Run("nil quote produces empty QuotedContext", func(t *testing.T) {
		req := buildIMQARequest(session, "hello", "a1", "u1", nil, nil, nil)
		if req.QuotedContext != "" {
			t.Errorf("QuotedContext = %q, want empty", req.QuotedContext)
		}
		if req.Query != "hello" {
			t.Errorf("Query = %q, want %q", req.Query, "hello")
		}
	})

	t.Run("bot quote sets QuotedContext with bot label", func(t *testing.T) {
		quote := &QuotedMessage{Content: "bot reply", IsBotMessage: true}
		req := buildIMQARequest(session, "follow up", "a1", "u1", nil, nil, quote)
		if req.Query != "follow up" {
			t.Errorf("Query = %q, want %q", req.Query, "follow up")
		}
		want := "以下是用户引用的你（机器人）之前的回复，仅作为上下文参考：\n<quoted_message>\nbot reply\n</quoted_message>"
		if req.QuotedContext != want {
			t.Errorf("QuotedContext = %q, want %q", req.QuotedContext, want)
		}
	})

	t.Run("user quote sets QuotedContext with user label", func(t *testing.T) {
		quote := &QuotedMessage{Content: "user msg", IsBotMessage: false}
		req := buildIMQARequest(session, "question", "a1", "u1", nil, nil, quote)
		want := "以下是用户引用的一条历史消息，仅作为上下文参考：\n<quoted_message>\nuser msg\n</quoted_message>"
		if req.QuotedContext != want {
			t.Errorf("QuotedContext = %q, want %q", req.QuotedContext, want)
		}
	})
}
