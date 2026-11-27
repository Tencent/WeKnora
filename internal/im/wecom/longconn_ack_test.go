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
	"github.com/stretchr/testify/require"
)

// Run the real authentication and receive loop against a local WeCom peer.
func newReplyAckClient(t *testing.T, respond func(*ws.Conn, wsFrame)) *LongConnClient {
	t.Helper()
	setWhitelistForTest(t, "127.0.0.1")
	upgrader := ws.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var auth wsFrame
		if conn.ReadJSON(&auth) != nil {
			return
		}
		if conn.WriteJSON(wsFrame{Headers: auth.Headers}) != nil {
			return
		}
		if conn.WriteJSON(wsFrame{
			Cmd: cmdMsgCallback, Headers: map[string]string{"req_id": "ready"},
			Body: json.RawMessage(`{
				"msgid":"ready","msgtype":"text","from":{"userid":"user"},"text":{"content":"hi"}
			}`),
		}) != nil {
			return
		}
		for {
			var frame wsFrame
			if conn.ReadJSON(&frame) != nil {
				return
			}
			respond(conn, frame)
		}
	}))
	t.Cleanup(server.Close)
	ready := make(chan struct{})
	client := &LongConnClient{
		endpoint: "ws" + strings.TrimPrefix(server.URL, "http"),
		handler: func(context.Context, *im.IncomingMessage) error {
			close(ready)
			return nil
		},
	}
	stopped := make(chan error, 1)
	go func() { stopped <- client.connectAndRun(context.Background()) }()
	t.Cleanup(func() {
		client.Stop()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Error("receive loop did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-stopped:
		t.Fatalf("connection failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not arrive")
	}
	return client
}

func TestReplyMethodsReportWeComRejection(t *testing.T) {
	for _, method := range []string{"reply", "finalize", "end"} {
		t.Run(method, func(t *testing.T) {
			client := newReplyAckClient(t, func(conn *ws.Conn, frame wsFrame) {
				require.NoError(t, conn.WriteJSON(wsFrame{
					Headers: frame.Headers, ErrCode: 45009, ErrMsg: "reply rejected",
				}))
			})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			msg := &im.IncomingMessage{Extra: map[string]string{"req_id": "request"}}
			var err error
			if method == "reply" {
				err = client.SendReply(ctx, msg, &im.ReplyMessage{Content: "complete answer"})
			} else {
				streamID, startErr := client.StartStream(ctx, msg)
				require.NoError(t, startErr)
				if method == "finalize" {
					err = client.FinalizeStream(ctx, msg, streamID, "complete answer")
				} else {
					err = client.EndStream(ctx, msg, streamID)
				}
			}
			require.ErrorContains(t, err, "45009")
			require.ErrorContains(t, err, "reply rejected")
		})
	}
}

func TestStreamFinalFrameContainsTheCompleteAnswer(t *testing.T) {
	final := make(chan streamReplyBody, 1)
	client := newReplyAckClient(t, func(conn *ws.Conn, frame wsFrame) {
		var body streamReplyBody
		require.NoError(t, json.Unmarshal(frame.Body, &body))
		if body.Stream.Finish {
			final <- body
		}
		require.NoError(t, conn.WriteJSON(wsFrame{Headers: frame.Headers}))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msg := &im.IncomingMessage{Extra: map[string]string{"req_id": "request"}}
	streamID, err := client.StartStream(ctx, msg)
	require.NoError(t, err)
	require.NoError(t, client.UpdateStreamContent(ctx, msg, streamID, "partial"))
	require.NoError(t, client.FinalizeStream(ctx, msg, streamID, "complete answer"))
	require.NoError(t, client.EndStream(ctx, msg, streamID))
	select {
	case body := <-final:
		require.Equal(t, "complete answer", body.Stream.Content)
		require.Equal(t, streamID, body.Stream.ID)
	case <-ctx.Done():
		t.Fatal("final frame missing")
	}
}

func TestReplyAckWaitEndsOnCancellationOrDisconnect(t *testing.T) {
	for _, mode := range []string{"cancellation", "disconnect", "stop", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			received := make(chan struct{})
			client := newReplyAckClient(t, func(conn *ws.Conn, _ wsFrame) {
				close(received)
				if mode == "disconnect" {
					_ = conn.Close()
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- client.SendReply(ctx,
					&im.IncomingMessage{Extra: map[string]string{"req_id": "request"}},
					&im.ReplyMessage{Content: "answer"})
			}()
			select {
			case <-received:
			case <-time.After(3 * time.Second):
				t.Fatal("reply was not sent")
			}
			if mode == "cancellation" {
				cancel()
			}
			if mode == "stop" {
				client.Stop()
			}
			select {
			case err := <-result:
				require.Error(t, err)
				if mode == "cancellation" {
					require.ErrorIs(t, err, context.Canceled)
				}
				if mode == "timeout" {
					require.ErrorContains(t, err, "timed out")
				}
			case <-time.After(replyAckTimeout + time.Second):
				t.Fatal("reply ACK wait did not finish")
			}
			client.mu.Lock()
			pendingCount, conn := len(client.pendingReplies), client.conn
			client.mu.Unlock()
			require.Zero(t, pendingCount)
			require.Nil(t, conn)
		})
	}
}

func TestReplyAcksAreMatchedByRequestAndConnection(t *testing.T) {
	active := newTestConn(t)
	old := newTestConn(t)
	pending := &replyAck{conn: active, result: make(chan error, 1)}
	client := &LongConnClient{pendingReplies: map[string]*replyAck{"current": pending}}
	client.receiveReplyAck(old, wsFrame{Headers: map[string]string{"req_id": "current"}})
	client.receiveReplyAck(active, wsFrame{Headers: map[string]string{"req_id": "other"}})
	require.Empty(t, pending.result)
	client.receiveReplyAck(active, wsFrame{Headers: map[string]string{"req_id": "current"}})
	select {
	case err := <-pending.result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("matching ACK was ignored")
	}
}

func TestReplyAcksSerializeOnlyTheSameRequest(t *testing.T) {
	firstReceived := make(chan struct{})
	client := newReplyAckClient(t, func(conn *ws.Conn, frame wsFrame) {
		var body streamReplyBody
		require.NoError(t, json.Unmarshal(frame.Body, &body))
		switch body.Stream.Content {
		case "first":
			close(firstReceived) // hold this ACK while other requests can proceed
		case "other":
			require.NoError(t, conn.WriteJSON(wsFrame{Headers: frame.Headers}))
			require.NoError(t, conn.WriteJSON(wsFrame{Headers: map[string]string{"req_id": "same"}}))
		case "second":
			require.NoError(t, conn.WriteJSON(wsFrame{
				Headers: frame.Headers, ErrCode: 45009, ErrMsg: "second rejected",
			}))
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msg := &im.IncomingMessage{Extra: map[string]string{"req_id": "same"}}
	firstResult := make(chan error, 1)
	go func() { firstResult <- client.SendReply(ctx, msg, &im.ReplyMessage{Content: "first"}) }()
	select {
	case <-firstReceived:
	case <-ctx.Done():
		t.Fatal("first reply missing")
	}
	queuedCtx, queuedCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer queuedCancel()
	require.ErrorIs(t, client.SendReply(queuedCtx, msg, &im.ReplyMessage{Content: "cancelled"}),
		context.DeadlineExceeded)
	secondResult := make(chan error, 1)
	go func() { secondResult <- client.SendReply(ctx, msg, &im.ReplyMessage{Content: "second"}) }()
	require.NoError(t, client.SendReply(ctx,
		&im.IncomingMessage{Extra: map[string]string{"req_id": "other"}},
		&im.ReplyMessage{Content: "other"}))
	require.NoError(t, <-firstResult)
	require.ErrorContains(t, <-secondResult, "second rejected")
}

func TestFinalizeReportsTerminalRejectionForFallback(t *testing.T) {
	client := newReplyAckClient(t, func(conn *ws.Conn, frame wsFrame) {
		var body streamReplyBody
		require.NoError(t, json.Unmarshal(frame.Body, &body))
		ack := wsFrame{Headers: frame.Headers}
		if body.Stream.Finish {
			ack.ErrCode, ack.ErrMsg = 45009, "terminal rejected"
		}
		require.NoError(t, conn.WriteJSON(ack))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	msg := &im.IncomingMessage{Extra: map[string]string{"req_id": "request"}}
	streamID, err := client.StartStream(ctx, msg)
	require.NoError(t, err)
	require.ErrorContains(t, client.FinalizeStream(ctx, msg, streamID, "complete answer"), "terminal rejected")
	// The service always calls EndStream after FinalizeStream. Cleanup must
	// be idempotent so it cannot send an empty replacement after finalization.
	require.NoError(t, client.EndStream(ctx, msg, streamID))
}

func TestReplyWaitsForAuthenticationBeforeSending(t *testing.T) {
	client := &LongConnClient{conn: newTestConn(t)}
	err := client.SendReply(context.Background(),
		&im.IncomingMessage{Extra: map[string]string{"req_id": "request"}},
		&im.ReplyMessage{Content: "answer"})
	require.ErrorContains(t, err, "not ready")
	require.Empty(t, client.pendingReplies)
}
