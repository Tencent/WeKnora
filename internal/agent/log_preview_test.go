package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreviewForLogKeepsRunesWhole(t *testing.T) {
	// 100 bytes into this string lands inside the 34th three-byte character:
	// the byte slice gives "解\xe6", which is not valid UTF-8.
	content := strings.Repeat("文档解析失败", 40)

	preview := previewForLog(content, 100, "...")

	if !utf8.ValidString(preview) {
		t.Fatalf("preview is not valid UTF-8: %q", preview)
	}
	body := strings.TrimSuffix(preview, "...")
	if n := utf8.RuneCountInString(body); n != 100 {
		t.Fatalf("preview body has %d runes, want 100", n)
	}
	if !strings.HasSuffix(preview, "...") {
		t.Fatalf("truncated preview should end with the marker: %q", preview)
	}
}

func TestPreviewForLogLeavesShortInputAlone(t *testing.T) {
	for _, s := range []string{"", "ok", "短文本", strings.Repeat("文", 100)} {
		if got := previewForLog(s, 100, "..."); got != s {
			t.Fatalf("previewForLog(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestPreviewForLogCapsEveryBudget(t *testing.T) {
	content := strings.Repeat("工具输出内容", 200)

	for _, n := range []int{messagePreviewRunes, llmContentPreviewRunes, toolOutputPreviewRunes} {
		preview := previewForLog(content, n, "... (truncated)")
		if !utf8.ValidString(preview) {
			t.Fatalf("budget %d: preview is not valid UTF-8: %q", n, preview)
		}
		body := strings.TrimSuffix(preview, "... (truncated)")
		if got := utf8.RuneCountInString(body); got != n {
			t.Fatalf("budget %d: preview body has %d runes, want %d", n, got, n)
		}
	}
}

func TestPreviewForLogHandlesNonPositiveBudget(t *testing.T) {
	content := strings.Repeat("文", 50)
	if got := previewForLog(content, 0, "..."); got != content {
		t.Fatalf("budget 0 should return the input unchanged, got %q", got)
	}
	if got := previewForLog(content, -1, "..."); got != content {
		t.Fatalf("negative budget should return the input unchanged, got %q", got)
	}
}
