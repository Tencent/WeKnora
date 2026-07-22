package yunzhijia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestVerifyCallbackSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	msg := callbackMessage{
		Type: 2, RobotID: "robot", RobotName: "WeKnora", OperatorOpenid: "user",
		OperatorName: "User", Time: 123, MsgID: "message", Content: "@WeKnora hello",
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader(body))
	req.Header.Set("Sign", computeSignature("secret", &msg))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	adapter := NewAdapter("https://www.yunzhijia.com/send", "secret", "", "", 10, "yunzhijia.com")
	if err := adapter.VerifyCallback(c); err != nil {
		t.Fatalf("VerifyCallback() error = %v", err)
	}
	parsed, err := adapter.ParseCallback(c)
	if err != nil {
		t.Fatalf("ParseCallback() error = %v", err)
	}
	if parsed == nil || parsed.Content != "hello" {
		t.Fatalf("parsed message = %#v, want content hello", parsed)
	}
}

func TestToIncomingMessageRequiresRobotMention(t *testing.T) {
	msg := &callbackMessage{
		Type: 2, RobotID: "robot", RobotName: "WeKnora", OperatorOpenid: "user",
		OperatorName: "User", Time: 123, MsgID: "message", Content: "hello",
	}
	if got := toIncomingMessage(t.Context(), msg); got != nil {
		t.Fatalf("toIncomingMessage() = %#v, want nil without robot mention", got)
	}
}

func TestCleanAtMentionRequiresNameBoundary(t *testing.T) {
	if _, mentioned := cleanAtMention("@WeKnoraPlus hello", "WeKnora"); mentioned {
		t.Fatal("longer user name must not be treated as a robot mention")
	}
	if got, mentioned := cleanAtMention("@WeKnora：你好", "WeKnora"); !mentioned || got != "你好" {
		t.Fatalf("cleanAtMention() = %q, %v; want 你好, true", got, mentioned)
	}
}

func TestToIncomingMessageParsesMsgParamImage(t *testing.T) {
	msg := &callbackMessage{
		Type:           2,
		MsgType:        23,
		RobotID:        "BOT-1",
		RobotName:      "Websocket",
		GroupID:        "group-1",
		OperatorOpenid: "user",
		OperatorName:   "User",
		Time:           123,
		MsgID:          "message",
		Content:        "@Websocket 图片测试一下看看[图片]",
		GroupType:      2,
		MsgParam: `{"desc":[{"data":"BOT-1","length":10,"start":0,"type":"at"},` +
			`{"data":"file-1","h":1032,"length":4,"start":19,"type":"image","w":1920}],` +
			`"notifyTo":["BOT-1"],"notifyType":1}`,
	}

	got := toIncomingMessage(t.Context(), msg)
	if got == nil {
		t.Fatal("toIncomingMessage() returned nil")
	}
	if got.MessageType != im.MessageTypeImage {
		t.Fatalf("MessageType = %q, want image", got.MessageType)
	}
	if got.FileKey != "file-1" {
		t.Fatalf("FileKey = %q, want file-1", got.FileKey)
	}
	if got.FileName != "message.png" {
		t.Fatalf("FileName = %q, want message.png", got.FileName)
	}
	if got.ChatID != "group-1" {
		t.Fatalf("ChatID = %q, want group-1", got.ChatID)
	}
	if got.Content != "图片测试一下看看[图片]" {
		t.Fatalf("Content = %q", got.Content)
	}
	if got.Extra["yunzhijia_image_width"] != "1920" || got.Extra["yunzhijia_image_height"] != "1032" {
		t.Fatalf("image dimensions extra = %#v", got.Extra)
	}
}

func TestToIncomingMessageAcceptsMsgParamMention(t *testing.T) {
	msg := &callbackMessage{
		Type:           2,
		RobotID:        "BOT-1",
		RobotName:      "Websocket",
		OperatorOpenid: "user",
		OperatorName:   "User",
		Time:           123,
		MsgID:          "message",
		Content:        "图片测试一下看看[图片]",
		MsgParam:       `{"desc":[{"data":"BOT-1","type":"at"},{"data":"file-1","type":"image"}]}`,
	}

	got := toIncomingMessage(t.Context(), msg)
	if got == nil {
		t.Fatal("toIncomingMessage() returned nil")
	}
	if got.FileKey != "file-1" {
		t.Fatalf("FileKey = %q, want file-1", got.FileKey)
	}
}

func TestToIncomingMessageAcceptsImageWithoutText(t *testing.T) {
	msg := &callbackMessage{
		Type:           2,
		RobotID:        "BOT-1",
		RobotName:      "Websocket",
		OperatorOpenid: "user",
		OperatorName:   "User",
		Time:           123,
		MsgID:          "message",
		Content:        "",
		MsgParam:       `{"desc":[{"data":"BOT-1","type":"at"},{"data":"file-1","type":"image"}]}`,
	}

	got := toIncomingMessage(t.Context(), msg)
	if got == nil {
		t.Fatal("toIncomingMessage() returned nil")
	}
	if got.MessageType != im.MessageTypeImage || got.FileKey != "file-1" || got.Content != "" {
		t.Fatalf("incoming = %#v", got)
	}
}

func TestSendReplyAcceptsAny2xxAndBuildsPayload(t *testing.T) {
	adapter := NewAdapter("https://www.yunzhijia.com/send", "", "", "", 10, "yunzhijia.com")
	var payload sendMessagePayload
	adapter.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Header:     make(http.Header),
		}, nil
	})}

	incoming := &im.IncomingMessage{UserID: "user", Extra: map[string]string{"group_type": "1"}}
	if err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: "answer"}); err != nil {
		t.Fatalf("SendReply() error = %v", err)
	}
	if payload.MsgType != textMessageType || payload.Content != "answer" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.NotifyParams) != 1 || len(payload.NotifyParams[0].Values) != 1 || payload.NotifyParams[0].Values[0] != "user" {
		t.Fatalf("notify params = %#v", payload.NotifyParams)
	}
	if payload.Param == nil || payload.Param.FormatType != markdownFormatType {
		t.Fatalf("expected markdown format param by default, got %#v", payload.Param)
	}
}

func TestSendReplyFormatTypeOverride(t *testing.T) {
	adapter := NewAdapter("https://www.yunzhijia.com/send", "", "", "", 10, "yunzhijia.com")
	var payload sendMessagePayload
	adapter.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Header:     make(http.Header),
		}, nil
	})}

	incoming := &im.IncomingMessage{UserID: "user"}

	// Empty string explicitly disables the param.
	if err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{
		Content: "answer",
		Extra:   map[string]string{"yunzhijia_format_type": ""},
	}); err != nil {
		t.Fatalf("SendReply() error = %v", err)
	}
	if payload.Param != nil {
		t.Fatalf("expected no param when format type overridden to empty, got %#v", payload.Param)
	}

	// Non-empty override replaces the default markdown formatType.
	if err := adapter.SendReply(context.Background(), incoming, &im.ReplyMessage{
		Content: "answer",
		Extra:   map[string]string{"yunzhijia_format_type": "text"},
	}); err != nil {
		t.Fatalf("SendReply() error = %v", err)
	}
	if payload.Param == nil || payload.Param.FormatType != "text" {
		t.Fatalf("expected overridden format type 'text', got %#v", payload.Param)
	}
}

func TestBuildDownloadFileURL(t *testing.T) {
	got := buildDownloadFileURL("file-1")
	if got != "https://yunzhijia.com/gateway/docrest/doc/file/downloadfileOpen?fileId=file-1" {
		t.Fatalf("download URL = %q", got)
	}
}

func TestDownloadFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth2_v12/auth/getAppAccessToken":
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s", r.Method)
			}
			var tokenReq map[string]any
			if err := json.NewDecoder(r.Body).Decode(&tokenReq); err != nil {
				t.Fatalf("decode token request: %v", err)
			}
			if tokenReq["appId"] != "app-id" || tokenReq["secret"] != "app-secret" {
				t.Fatalf("token request = %#v", tokenReq)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"accessToken":"token-1","expireIn":7136},"error":null,"errorCode":0,"success":true}`))
		case "/gateway/docrest/doc/file/downloadfileOpen":
			if r.Header.Get("Authorization") != "Bearer token-1" {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			if r.URL.Query().Get("fileId") != "file-1" {
				t.Fatalf("query = %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("image-bytes"))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	oldAuthURL := yunzhijiaAuthURL
	oldBaseURL := yunzhijiaDownloadFileBaseURL
	oldValidator := validateDownloadFileURL
	yunzhijiaAuthURL = server.URL + "/api/oauth2_v12/auth/getAppAccessToken"
	yunzhijiaDownloadFileBaseURL = server.URL + "/gateway/docrest/doc/file/downloadfileOpen"
	validateDownloadFileURL = func(string) error { return nil }
	defer func() {
		yunzhijiaAuthURL = oldAuthURL
		yunzhijiaDownloadFileBaseURL = oldBaseURL
		validateDownloadFileURL = oldValidator
	}()

	adapter := NewAdapter("https://www.yunzhijia.com/send", "", "app-id", "app-secret", 10, "yunzhijia.com")
	adapter.httpClient = server.Client()
	reader, fileName, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey:  "file-1",
		FileName: "message.png",
	})
	if err != nil {
		t.Fatalf("DownloadFile() error = %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "image-bytes" || fileName != "message.png" {
		t.Fatalf("body=%q fileName=%q", string(body), fileName)
	}
}

func TestDownloadFileRequiresAppCredentials(t *testing.T) {
	adapter := NewAdapter("https://www.yunzhijia.com/send", "", "", "", 10, "yunzhijia.com")
	_, _, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{FileKey: "file-1"})
	if err == nil || !strings.Contains(err.Error(), "app_id and app_secret") {
		t.Fatalf("DownloadFile() error = %v, want app credential error", err)
	}
}

func TestValidateFileIDRejectsURLishValues(t *testing.T) {
	for _, fileID := range []string{"", "a/b", "a?b", "a&b", "a b"} {
		if err := validateFileID(fileID); err == nil {
			t.Fatalf("validateFileID(%q) = nil, want error", fileID)
		}
	}
	if err := validateFileID("6a605afe5dda8e000121fcea"); err != nil {
		t.Fatalf("validateFileID(valid) error = %v", err)
	}
}
