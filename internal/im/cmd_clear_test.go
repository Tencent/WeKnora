package im

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestClearCommandSpeaksTheChannelLanguage pins the confirmation copy: a
// zh-TW / zh-Hant channel gets Traditional Chinese, everything else keeps the
// upstream Simplified string. /clear is the only session-reset command, so its
// confirmation is the one users see.
func TestClearCommandSpeaksTheChannelLanguage(t *testing.T) {
	cases := []struct {
		name    string
		lang    string
		wantSub string
		wantNot string
	}{
		{"traditional taiwan", "zh-TW", "對話已清空", "对话已清空"},
		{"traditional hant alias", "zh-Hant-TW", "對話已清空", "对话已清空"},
		{"simplified", "zh-CN", "对话已清空", "對話已清空"},
		{"other language keeps upstream copy", "en-US", "对话已清空", "對話已清空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.LanguageContextKey, tc.lang)
			result, err := newClearCommand().Execute(ctx, &CommandContext{}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(result.Content, tc.wantSub) {
				t.Errorf("content %q does not contain %q", result.Content, tc.wantSub)
			}
			if strings.Contains(result.Content, tc.wantNot) {
				t.Errorf("content %q must not contain %q", result.Content, tc.wantNot)
			}
			if result.Action != ActionClear {
				t.Errorf("action = %v, want ActionClear", result.Action)
			}
		})
	}
}
