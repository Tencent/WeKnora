package im

import (
	"testing"
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
			name:  "empty content",
			quote: &QuotedMessage{Content: ""},
			want:  "",
		},
		{
			name:  "bot message",
			quote: &QuotedMessage{Content: "bot reply text", IsBotMessage: true},
			want:  "[引用的机器人回复]\nbot reply text",
		},
		{
			name:  "user message",
			quote: &QuotedMessage{Content: "user message text", IsBotMessage: false},
			want:  "[被引用的消息]\nuser message text",
		},
		{
			name: "truncation at 500 runes",
			quote: &QuotedMessage{
				Content:      string(make([]rune, 600)),
				IsBotMessage: false,
			},
			want: "[被引用的消息]\n" + string(make([]rune, 500)) + "...",
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
