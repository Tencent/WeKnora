package dingtalk

import (
	"encoding/json"
	"testing"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/Tencent/WeKnora/internal/im"
)

// Compile-time check: the adapter must implement FileDownloader so the IM
// service dispatches file messages to it (issue #1771).
var _ im.FileDownloader = (*Adapter)(nil)

func TestParseFileContent_File(t *testing.T) {
	content := json.RawMessage(`{"spaceId":"223573","fileName":"foobar.zip","downloadCode":"LJYYbw==","fileId":"117848"}`)
	msgType, fileName, downloadCode, ok := parseFileContent("file", content)
	if !ok {
		t.Fatalf("expected ok=true for file message")
	}
	if msgType != im.MessageTypeFile {
		t.Errorf("msgType = %q, want %q", msgType, im.MessageTypeFile)
	}
	if fileName != "foobar.zip" {
		t.Errorf("fileName = %q, want %q", fileName, "foobar.zip")
	}
	if downloadCode != "LJYYbw==" {
		t.Errorf("downloadCode = %q, want %q", downloadCode, "LJYYbw==")
	}
}

func TestParseFileContent_Picture(t *testing.T) {
	content := json.RawMessage(`{"pictureDownloadCode":"pWjAks=","downloadCode":"mIofJE0E"}`)
	msgType, fileName, downloadCode, ok := parseFileContent("picture", content)
	if !ok {
		t.Fatalf("expected ok=true for picture message")
	}
	if msgType != im.MessageTypeImage {
		t.Errorf("msgType = %q, want %q", msgType, im.MessageTypeImage)
	}
	if fileName != "" {
		t.Errorf("fileName = %q, want empty (pipeline appends extension)", fileName)
	}
	if downloadCode != "mIofJE0E" {
		t.Errorf("downloadCode = %q, want %q", downloadCode, "mIofJE0E")
	}
}

func TestParseFileContent_Text(t *testing.T) {
	_, _, _, ok := parseFileContent("text", nil)
	if ok {
		t.Errorf("expected ok=false for text message")
	}
}

func TestParseFileContent_EmptyDownloadCode(t *testing.T) {
	_, _, _, ok := parseFileContent("file", json.RawMessage(`{"fileName":"a.pdf"}`))
	if ok {
		t.Errorf("expected ok=false when downloadCode is missing")
	}
	_, _, _, ok = parseFileContent("picture", json.RawMessage(`{}`))
	if ok {
		t.Errorf("expected ok=false when picture has no download code")
	}
}

func TestParseFileContent_PictureDownloadCodeFallback(t *testing.T) {
	content := json.RawMessage(`{"pictureDownloadCode":"pWjAks="}`)
	msgType, fileName, downloadCode, ok := parseFileContent("picture", content)
	if !ok {
		t.Fatalf("expected ok=true when only pictureDownloadCode is present")
	}
	if msgType != im.MessageTypeImage {
		t.Errorf("msgType = %q, want %q", msgType, im.MessageTypeImage)
	}
	if fileName != "" {
		t.Errorf("fileName = %q, want empty", fileName)
	}
	if downloadCode != "pWjAks=" {
		t.Errorf("downloadCode = %q, want %q", downloadCode, "pWjAks=")
	}
}

func TestParseFileContent_InvalidJSON(t *testing.T) {
	_, _, _, ok := parseFileContent("file", json.RawMessage(`not-json`))
	if ok {
		t.Errorf("expected ok=false for malformed content JSON")
	}
}

func TestDefaultFileName(t *testing.T) {
	if got := defaultFileName(im.MessageTypeImage, "", "pic-1"); got != "pic-1.png" {
		t.Errorf("image default = %q, want pic-1.png", got)
	}
	if got := defaultFileName(im.MessageTypeFile, "spec.pdf", "m1"); got != "spec.pdf" {
		t.Errorf("file with name = %q, want spec.pdf", got)
	}
	if got := defaultFileName(im.MessageTypeFile, "", "m2"); got != "m2" {
		t.Errorf("file without name = %q, want m2", got)
	}
}

func TestParseDownloadURL(t *testing.T) {
	url, err := parseDownloadURL(json.RawMessage(`{"downloadUrl":"https://example.com/file?token=abc"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/file?token=abc" {
		t.Errorf("url = %q, want %q", url, "https://example.com/file?token=abc")
	}
}

func TestParseDownloadURL_Missing(t *testing.T) {
	_, err := parseDownloadURL(json.RawMessage(`{}`))
	if err == nil {
		t.Errorf("expected error when downloadUrl is missing")
	}
}

func TestParseCallbackMessage_File(t *testing.T) {
	msg := &callbackMessage{
		MsgID:            "m1",
		Msgtype:          "file",
		RobotCode:        "robot-123",
		SenderStaffId:    "staff-1",
		SenderNick:       "Alice",
		ConversationType: "1",
		Content:          json.RawMessage(`{"fileName":"spec.pdf","downloadCode":"CODE1","spaceId":"s1"}`),
	}
	got := parseCallbackMessage(msg)
	if got.MessageType != im.MessageTypeFile {
		t.Errorf("MessageType = %q, want %q", got.MessageType, im.MessageTypeFile)
	}
	if got.FileName != "spec.pdf" {
		t.Errorf("FileName = %q, want %q", got.FileName, "spec.pdf")
	}
	if got.FileKey != "CODE1" {
		t.Errorf("FileKey = %q, want %q", got.FileKey, "CODE1")
	}
	if got.Extra["robot_code"] != "robot-123" {
		t.Errorf("Extra[robot_code] = %q, want %q", got.Extra["robot_code"], "robot-123")
	}
}

func TestParseCallbackMessage_Picture(t *testing.T) {
	msg := &callbackMessage{
		MsgID:     "pic-1",
		Msgtype:   "picture",
		RobotCode: "robot-123",
		Content:   json.RawMessage(`{"downloadCode":"PCODE"}`),
	}
	got := parseCallbackMessage(msg)
	if got.MessageType != im.MessageTypeImage {
		t.Errorf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
	// Pictures carry no filename; derive one from the message ID so the KB entry
	// has a real stem (matches the WeCom adapter behavior).
	if got.FileName != "pic-1.png" {
		t.Errorf("FileName = %q, want %q", got.FileName, "pic-1.png")
	}
	if got.FileKey != "PCODE" {
		t.Errorf("FileKey = %q, want %q", got.FileKey, "PCODE")
	}
}

func TestParseCallbackMessage_TextStillWorks(t *testing.T) {
	msg := &callbackMessage{
		MsgID:         "m2",
		Msgtype:       "text",
		SenderStaffId: "staff-1",
		Text:          &textContent{Content: "  hello  "},
	}
	got := parseCallbackMessage(msg)
	if got.MessageType != im.MessageTypeText {
		t.Errorf("MessageType = %q, want %q", got.MessageType, im.MessageTypeText)
	}
	if got.Content != "hello" {
		t.Errorf("Content = %q, want %q", got.Content, "hello")
	}
}

func TestStreamToIncoming_File(t *testing.T) {
	data := &chatbot.BotCallbackDataModel{
		MsgId:            "m3",
		Msgtype:          "file",
		SenderStaffId:    "staff-2",
		SenderNick:       "Bob",
		ConversationType: "1",
		Content: map[string]interface{}{
			"fileName":     "notes.docx",
			"downloadCode": "CODE2",
		},
	}
	got := streamToIncoming(data, "client-app-key")
	if got.MessageType != im.MessageTypeFile {
		t.Errorf("MessageType = %q, want %q", got.MessageType, im.MessageTypeFile)
	}
	if got.FileName != "notes.docx" {
		t.Errorf("FileName = %q, want %q", got.FileName, "notes.docx")
	}
	if got.FileKey != "CODE2" {
		t.Errorf("FileKey = %q, want %q", got.FileKey, "CODE2")
	}
	// Stream mode has no robotCode field; fall back to the app client ID.
	if got.Extra["robot_code"] != "client-app-key" {
		t.Errorf("Extra[robot_code] = %q, want %q", got.Extra["robot_code"], "client-app-key")
	}
}

func TestStreamToIncoming_Picture(t *testing.T) {
	data := &chatbot.BotCallbackDataModel{
		MsgId:   "spic-1",
		Msgtype: "picture",
		Content: map[string]interface{}{"downloadCode": "SPCODE"},
	}
	got := streamToIncoming(data, "client-app-key")
	if got.MessageType != im.MessageTypeImage {
		t.Errorf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
	if got.FileName != "spic-1.png" {
		t.Errorf("FileName = %q, want %q", got.FileName, "spic-1.png")
	}
	if got.FileKey != "SPCODE" {
		t.Errorf("FileKey = %q, want %q", got.FileKey, "SPCODE")
	}
}

func TestStreamToIncoming_TextStillWorks(t *testing.T) {
	data := &chatbot.BotCallbackDataModel{
		MsgId:         "m4",
		Msgtype:       "text",
		SenderStaffId: "staff-2",
		Text:          chatbot.BotCallbackDataTextModel{Content: " hi "},
	}
	got := streamToIncoming(data, "client-app-key")
	if got.MessageType != im.MessageTypeText {
		t.Errorf("MessageType = %q, want %q", got.MessageType, im.MessageTypeText)
	}
	if got.Content != "hi" {
		t.Errorf("Content = %q, want %q", got.Content, "hi")
	}
}
