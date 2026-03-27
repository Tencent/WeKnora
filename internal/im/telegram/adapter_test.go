package telegram

import (
	"testing"

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
