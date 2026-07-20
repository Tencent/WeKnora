package yunzhijia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	adapter := NewAdapter("https://www.yunzhijia.com/send", "secret", 10, "yunzhijia.com")
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

func TestSendReplyAcceptsAny2xxAndBuildsPayload(t *testing.T) {
	adapter := NewAdapter("https://www.yunzhijia.com/send", "", 10, "yunzhijia.com")
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
}
