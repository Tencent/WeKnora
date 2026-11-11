package line

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
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
	adapter := NewWebhookAdapterWithFlex("token", "secret", true)
	if raw, _ := json.Marshal(flexMessage(answer, adapter.cardHeader())); len(raw) <= lineMaxFlexJSONBytes {
		t.Fatalf("test fixture is not oversized: %d bytes", len(raw))
	}
	messages := adapter.buildMessages(context.Background(), answer, false)
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

// TestFlexMessagesLongAnswerStaysFlex makes sure an answer worth a card still
// gets one, and that the card header carries the channel name.
func TestFlexMessagesLongAnswerStaysFlex(t *testing.T) {
	answer := "## " + strings.Repeat("記憶體混插規則", 10) + "\n\n" + strings.Repeat("• 全機以最慢的 DIMM 速度運作。\n", 12)
	adapter := NewWebhookAdapterWithFlex("token", "secret", true)
	adapter.cardTitle = "HPE 智能檢索助手"

	messages := adapter.buildMessages(context.Background(), answer, false)
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0]["type"] != "flex" {
		t.Fatalf("type = %v, want flex", messages[0]["type"])
	}
	if got := messages[0]["altText"]; got != "HPE 智能檢索助手" {
		t.Errorf("altText = %v, want the channel name", got)
	}
	header := messages[0]["contents"].(map[string]interface{})["header"].(map[string]interface{})
	title := header["contents"].([]interface{})[0].(map[string]interface{})["text"]
	if title != "HPE 智能檢索助手" {
		t.Errorf("card title = %v, want the channel name", title)
	}
}

// TestShortNoticeStaysPlainText pins the UX rule that one-line confirmations and
// errors are not wrapped in a card: a "/clear" confirmation or an error notice
// inside a card with a header and a source tag reads like a malfunction.
func TestShortNoticeStaysPlainText(t *testing.T) {
	adapter := NewWebhookAdapterWithFlex("token", "secret", true)
	for _, notice := range []string{"✅ 對話已清空，下次消息將開始全新會話。", "會話已重置 / Session cleared", "❌ 无法读取此附件，请重试或改用文字描述。"} {
		messages := adapter.buildMessages(context.Background(), notice, false)
		if len(messages) != 1 {
			t.Fatalf("%q: messages = %d, want 1", notice, len(messages))
		}
		if messages[0]["type"] != "text" {
			t.Errorf("%q: type = %v, want text", notice, messages[0]["type"])
		}
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

// TestCommandOutputIsNeverACard pins the rule that command and system output is
// plain text even when it is long enough to qualify for a card: /help listings
// are reference text, and a card would add a bot-name header and a source tag
// that mean nothing for a command listing.
func TestCommandOutputIsNeverACard(t *testing.T) {
	help := "可用指令：\n" + strings.Repeat("· `/clear` — 清空对话记忆\n", 40)
	if utf8.RuneCountInString(help) < lineFlexMinRunes {
		t.Fatalf("fixture is too short: %d runes", utf8.RuneCountInString(help))
	}
	adapter := NewWebhookAdapterWithFlex("token", "secret", true)

	if got := adapter.buildMessages(context.Background(), help, true); len(got) != 1 || got[0]["type"] != "text" {
		t.Errorf("command output must go out as plain text, got %v", got)
	}
	if got := adapter.buildMessages(context.Background(), help, false); len(got) != 1 || got[0]["type"] != "flex" {
		t.Errorf("answers of the same length should still be a card, got %v", got)
	}
}
