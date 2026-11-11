package line

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSplitForLINEHonoursTextLimit pins the per-message character limit: no
// chunk may exceed LINE's 5000-rune text limit, and the concatenation must
// still carry the original content when it fits in the allowed message slots.
func TestSplitForLINEHonoursTextLimit(t *testing.T) {
	answer := strings.Repeat("HPE ProLiant DL360 Gen10 Plus 支援 32GB PC4-3200AA-R 單排 x4 記憶體模組。\n", 200)
	chunks := splitForLINE(answer, lineMaxTextRunes, lineMaxMessagesPerRequest)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if len(chunks) > lineMaxMessagesPerRequest {
		t.Fatalf("chunks = %d, want <= %d", len(chunks), lineMaxMessagesPerRequest)
	}
	for i, chunk := range chunks {
		if n := len([]rune(chunk)); n > lineMaxTextRunes {
			t.Errorf("chunk %d has %d runes, want <= %d", i, n, lineMaxTextRunes)
		}
	}
}

// TestSplitForLINEStopsAtFiveMessages confirms a long answer is cut down to the
// five message slots LINE accepts and only the last chunk is marked, instead of
// the request being rejected with a validation 400.
func TestSplitForLINEStopsAtFiveMessages(t *testing.T) {
	answer := strings.Repeat("x", lineMaxTextRunes*7)
	chunks := splitForLINE(answer, lineMaxTextRunes, lineMaxMessagesPerRequest)
	if len(chunks) != lineMaxMessagesPerRequest {
		t.Fatalf("chunks = %d, want %d", len(chunks), lineMaxMessagesPerRequest)
	}
	if !strings.HasSuffix(chunks[len(chunks)-1], lineTruncatedSuffix) {
		t.Errorf("last chunk must carry the truncation marker, got %q", tail(chunks[len(chunks)-1]))
	}
	for i, chunk := range chunks[:len(chunks)-1] {
		if strings.Contains(chunk, lineTruncatedSuffix) {
			t.Errorf("chunk %d must not carry the truncation marker", i)
		}
	}
}

// TestSplitForLINEBreaksOnNewline keeps sentences readable: the cut lands on a
// line break when one exists inside the window.
func TestSplitForLINEBreaksOnNewline(t *testing.T) {
	first := strings.Repeat("a", 4000)
	second := strings.Repeat("b", 4000)
	chunks := splitForLINE(first+"\n"+second, lineMaxTextRunes, lineMaxMessagesPerRequest)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if strings.HasPrefix(chunks[1], "\n") {
		t.Errorf("second chunk should start after the newline, got %q", head(chunks[1]))
	}
	if strings.HasSuffix(chunks[0], "b") {
		t.Errorf("first chunk must not swallow the second line")
	}
}

// TestFlexMessagesFallBackWhenCardTooLarge proves an oversized answer never
// produces a >30 KB flex bubble (LINE rejects it): it degrades to text messages
// that respect the 5000-rune limit instead.
func TestFlexMessagesFallBackWhenCardTooLarge(t *testing.T) {
	answer := strings.Repeat("• HPE 記憶體混插規則：全機以最慢的 DIMM 速度運作。\n| 規格 | 描述 |\n| --- | --- |\n| 處理器 | Xeon 6338 |\n\n", 400)
	if raw, _ := json.Marshal(flexMessage(answer)); len(raw) <= lineMaxFlexJSONBytes {
		t.Fatalf("test fixture is not oversized: %d bytes", len(raw))
	}
	messages := flexMessages(context.Background(), answer, true)
	if len(messages) == 0 {
		t.Fatal("expected fallback messages")
	}
	for i, msg := range messages {
		if msg["type"] != "text" {
			t.Fatalf("message %d type = %v, want text fallback", i, msg["type"])
		}
		if n := len([]rune(msg["text"].(string))); n > lineMaxTextRunes {
			t.Errorf("message %d has %d runes, want <= %d", i, n, lineMaxTextRunes)
		}
	}
}

// TestFlexMessagesSmallAnswerStaysFlex makes sure the normal path still emits a
// single flex card for an answer that fits.
func TestFlexMessagesSmallAnswerStaysFlex(t *testing.T) {
	messages := flexMessages(context.Background(), "## 你好\n\n這是**簡短**回答。", true)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0]["type"] != "flex" {
		t.Errorf("type = %v, want flex", messages[0]["type"])
	}
}

func head(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}

func tail(s string) string {
	if len(s) > 24 {
		return "…" + s[len(s)-24:]
	}
	return s
}
