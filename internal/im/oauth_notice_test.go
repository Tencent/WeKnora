package im

import (
	"strings"
	"testing"
)

func TestFormatIMMCPAuthNotice(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		want     string
		contains []string
	}{
		{
			name:  "empty input",
			input: nil,
			want:  "",
		},
		{
			name:  "all blank names",
			input: []string{"", "  ", "\t"},
			want:  "",
		},
		{
			name:     "single service",
			input:    []string{"GitHub MCP"},
			contains: []string{"GitHub MCP", "OAuth 授权"},
		},
		{
			name:     "dedupe and trim",
			input:    []string{" A ", "A", " B", "  ", "B"},
			contains: []string{"A、B"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMMCPAuthNotice(tt.input)
			if tt.contains == nil {
				if got != tt.want {
					t.Fatalf("formatIMMCPAuthNotice() = %q, want %q", got, tt.want)
				}
				return
			}
			for _, sub := range tt.contains {
				if !strings.Contains(got, sub) {
					t.Fatalf("formatIMMCPAuthNotice() = %q, want substring %q", got, sub)
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
