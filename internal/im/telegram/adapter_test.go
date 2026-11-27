package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/im"
)

func TestParseTelegramMessage_ForumTopicThread(t *testing.T) {
	msg := &telegramMsg{
		MessageID:       100,
		MessageThreadID: 42, // Forum topic ID
		From:            &telegramUser{ID: 1001, FirstName: "Alice"},
		Chat:            telegramChat{ID: -9999, Type: "supergroup"},
		Text:            "hello",
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	if incoming.ThreadID != "42" {
		t.Errorf("ThreadID = %q, want %q", incoming.ThreadID, "42")
	}
}

func TestParseTelegramMessage_NonForumGroup(t *testing.T) {
	msg := &telegramMsg{
		MessageID:       200,
		MessageThreadID: 0, // not a Forum group
		From:            &telegramUser{ID: 1002, FirstName: "Bob"},
		Chat:            telegramChat{ID: -8888, Type: "group"},
		Text:            "hello",
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	// Non-Forum groups: ThreadID should be empty
	if incoming.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty for non-Forum group", incoming.ThreadID)
	}
}

func TestParseTelegramMessage_DirectMessage(t *testing.T) {
	msg := &telegramMsg{
		MessageID: 300,
		From:      &telegramUser{ID: 1003, FirstName: "Carol"},
		Chat:      telegramChat{ID: 1003, Type: "private"},
		Text:      "hi bot",
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	if incoming.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty for DM", incoming.ThreadID)
	}
	if incoming.ChatType != im.ChatTypeDirect {
		t.Errorf("ChatType = %q, want %q", incoming.ChatType, im.ChatTypeDirect)
	}
}

func TestParseTelegramMessage_NilMessage(t *testing.T) {
	incoming := parseTelegramMessage(nil)
	if incoming != nil {
		t.Error("expected nil for nil message")
	}
}

func TestParseTelegramMessage_Document(t *testing.T) {
	msg := &telegramMsg{
		MessageID:       400,
		MessageThreadID: 7, // in a Forum topic
		From:            &telegramUser{ID: 1004, FirstName: "Dave"},
		Chat:            telegramChat{ID: -7777, Type: "supergroup"},
		Document: &telegramDoc{
			FileID:   "doc-123",
			FileName: "report.pdf",
			FileSize: 2048,
		},
	}

	incoming := parseTelegramMessage(msg)
	if incoming == nil {
		t.Fatal("expected non-nil message")
	}

	if incoming.ThreadID != "7" {
		t.Errorf("ThreadID = %q, want %q", incoming.ThreadID, "7")
	}
	if incoming.MessageType != im.MessageTypeFile {
		t.Errorf("MessageType = %q, want %q", incoming.MessageType, im.MessageTypeFile)
	}
}

func TestParseUpdate_NilMessage(t *testing.T) {
	update := &telegramUpdate{UpdateID: 1, Message: nil}
	incoming := parseUpdate(update)
	if incoming != nil {
		t.Error("expected nil for nil update.Message")
	}
}

type apiCall struct {
	method string
	body   map[string]any
}

type apiTransport func(*http.Request) (*http.Response, error)

func (f apiTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubBotAPI answers Bot API calls in place of api.telegram.org and records
// them. Like Telegram, it rejects a text over the message limit and a text
// that is blank; accept can reject further calls.
func stubBotAPI(t *testing.T, accept func(apiCall) bool) *[]apiCall {
	t.Helper()
	previous := httpClient
	t.Cleanup(func() { httpClient = previous })

	var calls []apiCall
	httpClient = &http.Client{Transport: apiTransport(func(r *http.Request) (*http.Response, error) {
		call := apiCall{method: path.Base(r.URL.Path)}
		if err := json.NewDecoder(r.Body).Decode(&call.body); err != nil {
			t.Errorf("decode %s body: %v", call.method, err)
		}
		calls = append(calls, call)

		text, _ := call.body["text"].(string)
		response := `{"ok":true,"result":{}}`
		tooLong := len(utf16.Encode([]rune(text))) > maxMessageUnits
		if tooLong || strings.TrimSpace(text) == "" || (accept != nil && !accept(call)) {
			response = `{"ok":false,"description":"Bad Request"}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(response)),
		}, nil
	})}
	return &calls
}

func joinTexts(calls []apiCall) string {
	var text strings.Builder
	for _, call := range calls {
		part, _ := call.body["text"].(string)
		text.WriteString(part)
	}
	return text.String()
}

func TestSplitMessageTextKeepsEveryPartWithinTheLimit(t *testing.T) {
	for name, text := range map[string]string{
		"empty":           "",
		"short":           "short",
		"at the limit":    strings.Repeat("x", maxMessageUnits),
		"one over":        strings.Repeat("x", maxMessageUnits+1),
		"chinese":         strings.Repeat("图", 3*maxMessageUnits),
		"surrogate pairs": "x" + strings.Repeat("😀", maxMessageUnits),
		"lines":           strings.Repeat("中文答案 ✅\n", 1500),
	} {
		t.Run(name, func(t *testing.T) {
			parts := splitMessageText(text)
			if strings.Join(parts, "") != text {
				t.Fatal("text changed when splitting")
			}
			for _, part := range parts {
				if units := len(utf16.Encode([]rune(part))); units > maxMessageUnits || !utf8.ValidString(part) {
					t.Fatalf("part has %d UTF-16 units or is not valid UTF-8", units)
				}
			}
		})
	}
}

func TestSplitMessageTextSplitsOnlyWhenNeededAndPrefersLineBreaks(t *testing.T) {
	if parts := splitMessageText(strings.Repeat("x", maxMessageUnits)); len(parts) != 1 {
		t.Errorf("text at the limit split into %d parts", len(parts))
	}
	parts := splitMessageText(strings.Repeat("中文答案 ✅\n", 1500))
	if len(parts) < 2 {
		t.Fatalf("long text split into %d parts", len(parts))
	}
	for _, part := range parts[:len(parts)-1] {
		if !strings.HasSuffix(part, "\n") {
			t.Fatalf("part ends inside a line: %q", part[len(part)-16:])
		}
	}
}

func TestSendReplySplitsALongAnswer(t *testing.T) {
	calls := stubBotAPI(t, nil)
	answer := strings.Repeat("中文答案 ✅\n", 1500)

	err := NewWebhookAdapter("token", "").SendReply(context.Background(),
		&im.IncomingMessage{ChatID: "-100", ThreadID: "42"}, &im.ReplyMessage{Content: answer, IsFinal: true})
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if len(*calls) < 2 || joinTexts(*calls) != im.FormatIMDisplayContent(answer, im.StreamDisplayFinal) {
		t.Fatalf("answer lost or duplicated across %d messages", len(*calls))
	}
	for _, call := range *calls {
		body := call.body
		if call.method != "sendMessage" || body["chat_id"] != "-100" || body["message_thread_id"] != float64(42) {
			t.Fatalf("part sent outside the chat or topic: %s %v", call.method, body)
		}
	}
}

func TestFinalizeStreamDoesNotSendABlankLastPart(t *testing.T) {
	calls := stubBotAPI(t, nil)
	streamID := startTestStream(t)

	err := NewWebhookAdapter("token", "").FinalizeStream(context.Background(),
		&im.IncomingMessage{ChatID: "-100"}, streamID, strings.Repeat("x", maxMessageUnits)+"\n\n")
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}

	if len(*calls) != 1 || (*calls)[0].method != "editMessageText" {
		t.Fatalf("calls = %+v, want one edit of the placeholder", *calls)
	}
}

func TestSendMessageRetriesAPartTelegramCannotParseAsPlainText(t *testing.T) {
	calls := stubBotAPI(t, func(call apiCall) bool { return call.body["parse_mode"] == nil })

	err := NewWebhookAdapter("token", "").SendReply(context.Background(),
		&im.IncomingMessage{ChatID: "-100"}, &im.ReplyMessage{Content: "an *unclosed entity", IsFinal: true})
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if len(*calls) != 2 || (*calls)[1].body["parse_mode"] != nil || (*calls)[1].body["text"] != "an *unclosed entity" {
		t.Fatalf("calls = %+v, want a Markdown attempt and a plain retry", *calls)
	}
}

func startTestStream(t *testing.T) string {
	t.Helper()
	const streamID = "-100:7"
	streamsMu.Lock()
	streams[streamID] = &streamState{msgID: "7", chatID: "-100", createdAt: time.Now()}
	streamsMu.Unlock()
	t.Cleanup(func() {
		streamsMu.Lock()
		delete(streams, streamID)
		streamsMu.Unlock()
	})
	return streamID
}

func TestFinalizeStreamSendsTheRestOfALongAnswer(t *testing.T) {
	calls := stubBotAPI(t, nil)
	streamID := startTestStream(t)
	answer := strings.Repeat("中文答案 ✅\n", 1500)

	err := NewWebhookAdapter("token", "").FinalizeStream(context.Background(),
		&im.IncomingMessage{ChatID: "-100", ThreadID: "42"}, streamID, answer)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}

	if len(*calls) < 2 || joinTexts(*calls) != answer {
		t.Fatalf("answer lost or duplicated across %d calls", len(*calls))
	}
	if first := (*calls)[0]; first.method != "editMessageText" || first.body["message_id"] != float64(7) {
		t.Fatalf("first part did not replace the placeholder: %s %v", first.method, first.body)
	}
	for _, call := range (*calls)[1:] {
		if call.method != "sendMessage" || call.body["message_thread_id"] != float64(42) {
			t.Fatalf("later part not sent to the topic: %s %v", call.method, call.body)
		}
	}
}

func TestFinalizeStreamReportsAPartItCouldNotSend(t *testing.T) {
	// The IM service resends the answer as a plain reply when FinalizeStream
	// fails, so a lost part must not be reported as success
	stubBotAPI(t, func(call apiCall) bool { return call.method != "sendMessage" })
	streamID := startTestStream(t)

	err := NewWebhookAdapter("token", "").FinalizeStream(context.Background(),
		&im.IncomingMessage{ChatID: "-100"}, streamID, strings.Repeat("x", maxMessageUnits+1))
	if err == nil {
		t.Fatal("FinalizeStream reported success with a part unsent")
	}
}
