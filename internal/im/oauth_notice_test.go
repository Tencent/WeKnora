package im

import (
	"context"
	"strings"
	"testing"
)

// buildIMMCPAuthNotice with a nil OAuth manager falls back to a per-service
// console hint (no authorization URL). These cases exercise the dedup and
// empty-input handling without needing a live OAuth manager.
func TestBuildIMMCPAuthNotice(t *testing.T) {
	svc := &Service{} // nil oauthManager => console-hint fallback

	tests := []struct {
		name     string
		input    []imMCPAuthService
		want     string
		contains []string
	}{
		{
			name:  "empty input",
			input: nil,
			want:  "",
		},
		{
			name:  "all blank ids",
			input: []imMCPAuthService{{ID: "", Name: "x"}, {ID: "", Name: "y"}},
			want:  "",
		},
		{
			name:     "single service",
			input:    []imMCPAuthService{{ID: "svc-1", Name: "GitHub MCP"}},
			contains: []string{"GitHub MCP", "OAuth 授权"},
		},
		{
			name: "dedupe by id",
			input: []imMCPAuthService{
				{ID: "a", Name: "A"},
				{ID: "a", Name: "A"},
				{ID: "b", Name: "B"},
			},
			contains: []string{"A", "B"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.buildIMMCPAuthNotice(context.Background(), tt.input)
			if tt.contains == nil {
				if got != tt.want {
					t.Fatalf("buildIMMCPAuthNotice() = %q, want %q", got, tt.want)
				}
				return
			}
			for _, sub := range tt.contains {
				if !strings.Contains(got, sub) {
					t.Fatalf("buildIMMCPAuthNotice() = %q, want substring %q", got, sub)
				}
			}
		})
	}
}

func TestAppendIMAuthNotice(t *testing.T) {
	notice := "⚠️ 需要授权"
	tests := []struct {
		name   string
		body   string
		notice string
		want   string
	}{
		{"empty notice", "answer", "", "answer"},
		{"empty body", "", notice, notice},
		{"whitespace body", "  \n  ", notice, notice},
		{"append with blank line", "answer", notice, "answer\n\n" + notice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendIMAuthNotice(tt.body, tt.notice)
			if got != tt.want {
				t.Fatalf("appendIMAuthNotice(%q, %q) = %q, want %q", tt.body, tt.notice, got, tt.want)
			}
		})
	}
}

// echoedAuthNotice builds a realistic echoed notice block the way a stored
// answer from an earlier turn carries it: the fixed lead-in plus per-service
// bullets, mixing the URL and console-hint forms.
func echoedAuthNotice() string {
	return imMCPAuthNoticeHeader + "\n" +
		"• Redmine：https://example.com/api/v1/mcp-oauth/authorize?state=old\n" +
		"• GitHub MCP（请在 WeKnora 管理后台完成 OAuth 授权）"
}

func TestStripIMAuthNotices(t *testing.T) {
	echoed := echoedAuthNotice()
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "body without a notice is returned unchanged",
			body: "答案正文\n\n含空行与尾随空格  \n",
			want: "答案正文\n\n含空行与尾随空格  \n",
		},
		{
			name: "echoed notice mid-body is removed",
			body: "前文\n\n" + echoed + "\n\n后文",
			want: "前文\n\n后文",
		},
		{
			name: "notice at the start is removed",
			body: echoed + "\n\n后文",
			want: "后文",
		},
		{
			name: "notice at the end is removed",
			body: "前文\n\n" + echoed,
			want: "前文",
		},
		{
			name: "whole body is a notice",
			body: echoed,
			want: "",
		},
		{
			name: "multiple echoed blocks are all removed",
			body: "前文\n\n" + echoed + "\n\n中间\n\n" + echoed + "\n\n后文",
			want: "前文\n\n中间\n\n后文",
		},
		{
			name: "prose interrupts a notice block; later bullets stay",
			body: imMCPAuthNoticeHeader + "\n• A：https://a\n说明文字\n• B：https://b",
			want: "说明文字\n• B：https://b",
		},
		{
			name: "a paraphrased list without the lead-in is untouched",
			body: "• Redmine：https://example.com/authorize?state=x\n\n正文",
			want: "• Redmine：https://example.com/authorize?state=x\n\n正文",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripIMAuthNotices(tt.body)
			if got != tt.want {
				t.Fatalf("stripIMAuthNotices(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// TestIMAuthNoticeKeepsSingleSection is the regression test for the duplicated
// authorization entries on the WeChat channel: the model echoes the notice
// blocks stored with earlier answers, and the IM layer stacked another notice
// on top. After the fix, the final reply carries exactly one authorization
// section — this turn's.
func TestIMAuthNoticeKeepsSingleSection(t *testing.T) {
	svc := &Service{} // nil oauthManager => console-hint fallback
	echoed := echoedAuthNotice()
	body := "第一轮答案\n\n" + echoed + "\n\n第二轮答案\n\n" + echoed + "\n\n本轮答案"

	answer := appendIMAuthNotice(stripIMAuthNotices(body), svc.buildIMMCPAuthNotice(
		context.Background(), []imMCPAuthService{{ID: "svc-1", Name: "Redmine"}}))

	if got := strings.Count(answer, imMCPAuthNoticeHeader); got != 1 {
		t.Fatalf("answer carries %d authorization sections, want 1:\n%s", got, answer)
	}
	for _, want := range []string{"第一轮答案", "第二轮答案", "本轮答案", "• Redmine"} {
		if !strings.Contains(answer, want) {
			t.Errorf("answer is missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "state=old") {
		t.Errorf("stale echoed authorization link survived:\n%s", answer)
	}

	// A turn without pending services must still clear echoed blocks.
	if got := stripIMAuthNotices(body); strings.Contains(got, imMCPAuthNoticeHeader) {
		t.Errorf("echoed blocks survived a turn with no pending services:\n%s", got)
	}
}
