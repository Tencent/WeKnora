package yunzhijia

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	ws "github.com/gorilla/websocket"
)

const testBusinessMessage = `{
  "type": 2,
  "robotId": "bot-1",
  "robotName": "WeKnora",
  "operatorOpenid": "user-1",
  "operatorName": "User",
  "time": 1719648000000,
  "msgId": "msg-1",
  "content": "@WeKnora hello",
  "groupType": 1
}`

func TestParseWebSocketFrameDirectMessage(t *testing.T) {
	frame, err := parseWebSocketFrame([]byte(testBusinessMessage))
	if err != nil {
		t.Fatalf("parseWebSocketFrame() error = %v", err)
	}
	if frame.message == nil || frame.message.MsgID != "msg-1" {
		t.Fatalf("message = %#v, want msg-1", frame.message)
	}
}

func TestParseWebSocketFrameRobotMessageEnvelope(t *testing.T) {
	frame, err := parseWebSocketFrame([]byte(`{"type":"robotMessage","msg":` + testBusinessMessage + `}`))
	if err != nil {
		t.Fatalf("parseWebSocketFrame() error = %v", err)
	}
	if frame.message == nil || frame.message.OperatorOpenid != "user-1" {
		t.Fatalf("message = %#v, want user-1", frame.message)
	}
}

func TestParseWebSocketFrameBuildsDirectPushACK(t *testing.T) {
	frame, err := parseWebSocketFrame([]byte(`{"cmd":"directPush","needAck":true,"seq":42}`))
	if err != nil {
		t.Fatalf("parseWebSocketFrame() error = %v", err)
	}
	var ack struct {
		Cmd string `json:"cmd"`
		Seq int64  `json:"seq"`
	}
	if err := json.Unmarshal(frame.ack, &ack); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if ack.Cmd != "ack" || ack.Seq != 42 {
		t.Fatalf("ack = %#v, want cmd=ack seq=42", ack)
	}
}

func TestParseWebSocketFrameControlAndInvalid(t *testing.T) {
	frame, err := parseWebSocketFrame([]byte(`{"event":"pong"}`))
	if err != nil || frame.control != "pong" {
		t.Fatalf("control frame = %#v, err=%v", frame, err)
	}
	if _, err := parseWebSocketFrame([]byte(`{"unknown":true}`)); err == nil {
		t.Fatal("expected invalid frame error")
	}
}

func TestToIncomingMessageSharedByWebhookAndWebSocket(t *testing.T) {
	var msg callbackMessage
	if err := json.Unmarshal([]byte(testBusinessMessage), &msg); err != nil {
		t.Fatal(err)
	}
	incoming := toIncomingMessage(t.Context(), &msg)
	if incoming == nil {
		t.Fatal("toIncomingMessage() returned nil")
	}
	if incoming.Content != "hello" {
		t.Fatalf("content = %q, want hello", incoming.Content)
	}
	if incoming.MessageID != "msg-1" || incoming.UserID != "user-1" {
		t.Fatalf("incoming = %#v", incoming)
	}
}

func TestWebSocketReconnectDelayCaps(t *testing.T) {
	if got := webSocketReconnectDelay(-1); got != webSocketReconnectDelays[0] {
		t.Fatalf("negative attempt delay = %v", got)
	}
	last := webSocketReconnectDelays[len(webSocketReconnectDelays)-1]
	if got := webSocketReconnectDelay(100); got != last {
		t.Fatalf("capped delay = %v, want %v", got, last)
	}
}

func TestLongConnClientProcessesMessagesInOrder(t *testing.T) {
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	client := NewLongConnClient("wss://example.com/ws", func(_ context.Context, msg *im.IncomingMessage) error {
		started <- msg.MessageID
		if msg.MessageID == "first" {
			<-releaseFirst
		}
		return nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go client.handleMessages(ctx)
	client.messages <- &im.IncomingMessage{MessageID: "first"}
	client.messages <- &im.IncomingMessage{MessageID: "second"}

	select {
	case got := <-started:
		if got != "first" {
			t.Fatalf("first handled message = %q, want first", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first message was not handled")
	}
	select {
	case got := <-started:
		t.Fatalf("second message started before first completed: %q", got)
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case got := <-started:
		if got != "second" {
			t.Fatalf("second handled message = %q, want second", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second message was not handled")
	}
}

func TestLongConnClientAcknowledgesFrameAndStops(t *testing.T) {
	ackReceived := make(chan struct{})
	var ackOnce sync.Once
	upgrader := ws.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(ws.TextMessage, []byte(`{"cmd":"directPush","needAck":true,"seq":42}`)); err != nil {
			return
		}
		_, data, err := conn.ReadMessage()
		if err == nil && string(data) == `{"cmd":"ack","seq":42}` {
			ackOnce.Do(func() { close(ackReceived) })
		}
	}))
	defer server.Close()

	client := NewLongConnClient("ws"+strings.TrimPrefix(server.URL, "http"), func(context.Context, *im.IncomingMessage) error {
		return nil
	})
	dialer := &net.Dialer{}
	client.dialer.NetDialContext = dialer.DialContext

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- client.Start(ctx) }()

	select {
	case <-ackReceived:
	case <-time.After(time.Second):
		t.Fatal("websocket ACK was not received")
	}
	cancel()
	client.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() error after stop = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("websocket client did not stop promptly")
	}
}
