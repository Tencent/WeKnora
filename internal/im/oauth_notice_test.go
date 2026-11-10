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
