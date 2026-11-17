package wecom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	ws "github.com/gorilla/websocket"
)

func newReplyCaptureConn(t *testing.T) (*ws.Conn, <-chan wsFrame) {
	t.Helper()

	frames := make(chan wsFrame, 8)
	upgrader := ws.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = serverConn.Close() }()
		for {
			var frame wsFrame
			if err := serverConn.ReadJSON(&frame); err != nil {
				return
			}
			frames <- frame
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := ws.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, frames
}

func readReplyFrame(t *testing.T, frames <-chan wsFrame) wsFrame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply frame")
		return wsFrame{}
	}
}

func decodeReplyBody[T any](t *testing.T, frame wsFrame) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatalf("decode reply body: %v", err)
	}
	return body
}

func assertReplyRequest(t *testing.T, frame wsFrame) {
	t.Helper()
	if frame.Cmd != cmdResponse {
		t.Fatalf("cmd = %q, want %q", frame.Cmd, cmdResponse)
	}
	if frame.Headers["req_id"] != "req-image" {
		t.Fatalf("req_id = %q, want req-image", frame.Headers["req_id"])
	}
}

func TestSendReplyWithImageUsesMarkdown(t *testing.T) {
	conn, frames := newReplyCaptureConn(t)
	client := &LongConnClient{conn: conn}
	incoming := &im.IncomingMessage{Extra: map[string]string{"req_id": "req-image"}}
	content := "before\n![architecture](https://example.com/r/token.png)\nafter"

	if err := client.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: content}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	frame := readReplyFrame(t, frames)
	assertReplyRequest(t, frame)
	body := decodeReplyBody[markdownReplyBody](t, frame)
	if body.MsgType != "markdown" {
		t.Fatalf("msgtype = %q, want markdown", body.MsgType)
	}
	if body.Markdown.Content != content {
		t.Fatalf("markdown content = %q, want %q", body.Markdown.Content, content)
	}
}

func TestSendReplyWithoutImageKeepsStreamResponse(t *testing.T) {
	conn, frames := newReplyCaptureConn(t)
	client := &LongConnClient{conn: conn}
	incoming := &im.IncomingMessage{Extra: map[string]string{"req_id": "req-image"}}

	if err := client.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: "plain **text**"}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	frame := readReplyFrame(t, frames)
	assertReplyRequest(t, frame)
	body := decodeReplyBody[streamReplyBody](t, frame)
	if body.MsgType != "stream" || !body.Stream.Finish || body.Stream.Content != "plain **text**" {
		t.Fatalf("unexpected stream body: %+v", body)
	}
}

func TestFinalizeStreamWithImageSendsNoticeAndMarkdown(t *testing.T) {
	conn, frames := newReplyCaptureConn(t)
	client := &LongConnClient{conn: conn}
	incoming := &im.IncomingMessage{Extra: map[string]string{"req_id": "req-image"}}
	content := "answer\n![chart](https://example.com/r/token.jpg)"

	streamID, err := client.StartStream(context.Background(), incoming)
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if err := client.FinalizeStream(context.Background(), incoming, streamID, content); err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if err := client.EndStream(context.Background(), incoming, streamID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	noticeUpdate := decodeReplyBody[streamReplyBody](t, readReplyFrame(t, frames))
	if noticeUpdate.MsgType != "stream" || noticeUpdate.Stream.Finish ||
		noticeUpdate.Stream.Content != weComImageReplyNotice {
		t.Fatalf("unexpected notice update: %+v", noticeUpdate)
	}

	markdown := decodeReplyBody[markdownReplyBody](t, readReplyFrame(t, frames))
	if markdown.MsgType != "markdown" || markdown.Markdown.Content != content {
		t.Fatalf("unexpected markdown reply: %+v", markdown)
	}

	finished := decodeReplyBody[streamReplyBody](t, readReplyFrame(t, frames))
	if finished.MsgType != "stream" || !finished.Stream.Finish ||
		finished.Stream.Content != weComImageReplyNotice {
		t.Fatalf("unexpected stream finish: %+v", finished)
	}
}
