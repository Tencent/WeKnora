package wecom

import (
	"testing"
)

func TestExtractQuoteContent(t *testing.T) {
	tests := []struct {
		name string
		msg  *botMessage
		want string
	}{
		{
			name: "nil quote",
			msg:  nil,
			want: "",
		},
		{
			name: "text message",
			msg: &botMessage{MsgType: "text", Text: struct {
				Content string `json:"content"`
			}{Content: "hello world"}},
			want: "hello world",
		},
		{
			name: "voice with STT content",
			msg: &botMessage{MsgType: "voice", Voice: struct {
				Content string `json:"content"`
			}{Content: "speech text"}},
			want: "speech text",
		},
		{
			name: "voice without STT content",
			msg:  &botMessage{MsgType: "voice"},
			want: "",
		},
		{
			name: "image returns empty (discarded to prevent hallucination)",
			msg:  &botMessage{MsgType: "image"},
			want: "",
		},
		{
			name: "file returns empty",
			msg:  &botMessage{MsgType: "file"},
			want: "",
		},
		{
			name: "video returns empty",
			msg:  &botMessage{MsgType: "video"},
			want: "",
		},
		{
			name: "unknown type returns empty",
			msg:  &botMessage{MsgType: "location"},
			want: "",
		},
		{
			name: "mixed text and image keeps only text",
			msg: &botMessage{
				MsgType: "mixed",
				Mixed: struct {
					MsgItem []botMixedItem `json:"msg_item"`
				}{
					MsgItem: []botMixedItem{
						{MsgType: "text", Text: struct {
							Content string `json:"content"`
						}{Content: "part1"}},
						{MsgType: "image"},
						{MsgType: "text", Text: struct {
							Content string `json:"content"`
						}{Content: "part2"}},
					},
				},
			},
			want: "part1\npart2",
		},
		{
			name: "mixed with only images returns empty",
			msg: &botMessage{
				MsgType: "mixed",
				Mixed: struct {
					MsgItem []botMixedItem `json:"msg_item"`
				}{
					MsgItem: []botMixedItem{
						{MsgType: "image"},
					},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQuoteContent(tt.msg)
			if got != tt.want {
				t.Errorf("extractQuoteContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsQuoteFromBot(t *testing.T) {
	tests := []struct {
		name    string
		quote   *botMessage
		aiBotID string
		want    bool
	}{
		{
			name:    "nil quote",
			quote:   nil,
			aiBotID: "bot123",
			want:    false,
		},
		{
			name: "matching from.userid",
			quote: &botMessage{From: struct {
				UserID string `json:"userid"`
			}{UserID: "bot123"}, AiBotID: ""},
			aiBotID: "bot123",
			want:    true,
		},
		{
			name: "matching aibotid",
			quote: &botMessage{From: struct {
				UserID string `json:"userid"`
			}{UserID: "other"}, AiBotID: "bot123"},
			aiBotID: "bot123",
			want:    true,
		},
		{
			name: "no match",
			quote: &botMessage{From: struct {
				UserID string `json:"userid"`
			}{UserID: "user456"}, AiBotID: ""},
			aiBotID: "bot123",
			want:    false,
		},
		{
			name: "empty aiBotID",
			quote: &botMessage{From: struct {
				UserID string `json:"userid"`
			}{UserID: "bot123"}},
			aiBotID: "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isQuoteFromBot(tt.quote, tt.aiBotID)
			if got != tt.want {
				t.Errorf("isQuoteFromBot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildQuotedMessage(t *testing.T) {
	t.Run("nil quote returns nil", func(t *testing.T) {
		if got := buildQuotedMessage(nil, "bot1"); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("text quote builds correctly", func(t *testing.T) {
		msg := &botMessage{
			MsgID:   "msg-001",
			MsgType: "text",
			AiBotID: "bot1",
			From: struct {
				UserID string `json:"userid"`
			}{UserID: "bot1"},
			Text: struct {
				Content string `json:"content"`
			}{Content: "hello"},
		}
		got := buildQuotedMessage(msg, "bot1")
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if got.MessageID != "msg-001" {
			t.Errorf("MessageID = %q, want %q", got.MessageID, "msg-001")
		}
		if got.Content != "hello" {
			t.Errorf("Content = %q, want %q", got.Content, "hello")
		}
		if got.SenderID != "bot1" {
			t.Errorf("SenderID = %q, want %q", got.SenderID, "bot1")
		}
		if !got.IsBotMessage {
			t.Error("IsBotMessage = false, want true")
		}
		if got.NonTextType != "" {
			t.Errorf("NonTextType = %q, want empty for text quote", got.NonTextType)
		}
	})

	t.Run("image quote sets NonTextType", func(t *testing.T) {
		msg := &botMessage{
			MsgID:   "msg-img",
			MsgType: "image",
			From: struct {
				UserID string `json:"userid"`
			}{UserID: "user456"},
		}
		got := buildQuotedMessage(msg, "bot1")
		if got == nil {
			t.Fatal("expected non-nil for image quote")
		}
		if got.Content != "" {
			t.Errorf("Content = %q, want empty", got.Content)
		}
		if got.NonTextType != "image" {
			t.Errorf("NonTextType = %q, want %q", got.NonTextType, "image")
		}
	})

	t.Run("video quote sets NonTextType", func(t *testing.T) {
		msg := &botMessage{
			MsgID:   "msg-vid",
			MsgType: "video",
			From: struct {
				UserID string `json:"userid"`
			}{UserID: "user456"},
		}
		got := buildQuotedMessage(msg, "bot1")
		if got == nil {
			t.Fatal("expected non-nil for video quote")
		}
		if got.NonTextType != "video" {
			t.Errorf("NonTextType = %q, want %q", got.NonTextType, "video")
		}
	})

	t.Run("user quote is not bot", func(t *testing.T) {
		msg := &botMessage{
			MsgID:   "msg-002",
			MsgType: "text",
			From: struct {
				UserID string `json:"userid"`
			}{UserID: "user456"},
			Text: struct {
				Content string `json:"content"`
			}{Content: "question"},
		}
		got := buildQuotedMessage(msg, "bot1")
		if got == nil {
			t.Fatal("expected non-nil")
		}
		if got.IsBotMessage {
			t.Error("IsBotMessage = true, want false")
		}
	})
}
