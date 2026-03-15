// WeCom Intelligent Bot long connection client.
//
// Protocol reference: https://developer.work.weixin.qq.com/document/path/101463
// Node.js SDK reference: https://github.com/WecomTeam/aibot-node-sdk
//
// Flow:
//  1. Connect to wss://openws.work.weixin.qq.com
//  2. Send aibot_subscribe with bot_id + secret
//  3. Receive aibot_msg_callback / aibot_event_callback frames
//  4. Reply via aibot_respond_msg on the same WebSocket
//  5. Heartbeat via ping/pong every 30s
package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	ws "github.com/gorilla/websocket"
)

const (
	wecomWSEndpoint = "wss://openws.work.weixin.qq.com"

	cmdSubscribe     = "aibot_subscribe"
	cmdPing          = "ping"
	cmdMsgCallback   = "aibot_msg_callback"
	cmdEventCallback = "aibot_event_callback"
	cmdResponse      = "aibot_respond_msg"

	defaultHeartbeatInterval    = 30 * time.Second
	defaultReconnectBaseDelay   = 1 * time.Second
	defaultReconnectMaxDelay    = 30 * time.Second
	defaultMaxReconnectAttempts = -1 // infinite
)

// wsFrame is the JSON frame exchanged over the WeCom bot WebSocket.
type wsFrame struct {
	Cmd     string            `json:"cmd,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
	ErrCode int               `json:"errcode,omitempty"`
	ErrMsg  string            `json:"errmsg,omitempty"`
}

// botMessage is the body of an aibot_msg_callback frame.
type botMessage struct {
	MsgID      string `json:"msgid"`
	AiBotID    string `json:"aibotid"`
	ChatID     string `json:"chatid"`
	ChatType   string `json:"chattype"` // "single" or "group"
	MsgType    string `json:"msgtype"`  // "text", "image", "event", ...
	CreateTime int64  `json:"create_time"`
	From       struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
}

// streamReplyBody is the body for a streaming text reply.
type streamReplyBody struct {
	MsgType string `json:"msgtype"`
	Stream  struct {
		ID      string `json:"id"`
		Finish  bool   `json:"finish"`
		Content string `json:"content"`
	} `json:"stream"`
}

// MessageHandler is called when an IM message is received via long connection.
type MessageHandler func(ctx context.Context, msg *im.IncomingMessage) error

// LongConnClient manages a WeCom intelligent bot WebSocket long connection.
type LongConnClient struct {
	botID   string
	secret  string
	handler MessageHandler

	conn   *ws.Conn
	mu     sync.Mutex
	closed atomic.Bool
	reqSeq atomic.Int64
}

// NewLongConnClient creates a WeCom long connection client.
func NewLongConnClient(botID, secret string, handler MessageHandler) *LongConnClient {
	return &LongConnClient{
		botID:   botID,
		secret:  secret,
		handler: handler,
	}
}

// Start connects and runs the long connection loop. It reconnects automatically on failure.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] WeCom WebSocket connecting (bot_id=%s)...", c.botID)

	attempts := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := c.connectAndRun(ctx)
		if c.closed.Load() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		attempts++
		if defaultMaxReconnectAttempts >= 0 && attempts >= defaultMaxReconnectAttempts {
			return fmt.Errorf("max reconnect attempts reached: %w", err)
		}

		delay := reconnectDelay(attempts)
		logger.Warnf(ctx, "[WeCom] Connection lost (%v), reconnecting in %v (attempt %d)...", err, delay, attempts)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Stop gracefully closes the connection.
func (c *LongConnClient) Stop() {
	c.closed.Store(true)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// SendReply sends a text reply through the WebSocket connection.
// This is used by the IM service to reply to messages in long connection mode.
func (c *LongConnClient) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	reqID, ok := incoming.Extra["req_id"]
	if !ok || reqID == "" {
		return fmt.Errorf("missing req_id in incoming message extra")
	}

	// Generate a unique stream ID for this reply
	streamID := fmt.Sprintf("stream_%d", c.reqSeq.Add(1))

	body := streamReplyBody{MsgType: "stream"}
	body.Stream.ID = streamID
	body.Stream.Finish = true
	body.Stream.Content = reply.Content

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal reply body: %w", err)
	}

	frame := wsFrame{
		Cmd:     cmdResponse,
		Headers: map[string]string{"req_id": reqID},
		Body:    bodyBytes,
	}

	return c.writeJSON(frame)
}

func (c *LongConnClient) connectAndRun(ctx context.Context) error {
	conn, _, err := ws.DefaultDialer.DialContext(ctx, wecomWSEndpoint, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		_ = conn.Close()
	}()

	// Authenticate
	if err := c.authenticate(ctx); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}

	logger.Infof(ctx, "[IM] WeCom WebSocket connected successfully (bot_id=%s)", c.botID)

	// Start heartbeat
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()
	go c.heartbeatLoop(heartbeatCtx)

	// Message receive loop
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		var frame wsFrame
		if err := json.Unmarshal(message, &frame); err != nil {
			logger.Warnf(ctx, "[WeCom] Failed to unmarshal frame: %v", err)
			continue
		}

		switch frame.Cmd {
		case cmdMsgCallback, cmdEventCallback:
			// Detach from connection ctx so in-flight messages survive reconnects.
			go c.handleCallback(context.WithoutCancel(ctx), frame)
		default:
			// pong or other control frames — ignore
		}
	}
}

func (c *LongConnClient) authenticate(ctx context.Context) error {
	authBody, _ := json.Marshal(map[string]string{
		"bot_id": c.botID,
		"secret": c.secret,
	})

	reqID := fmt.Sprintf("%s_%d", cmdSubscribe, time.Now().UnixNano())
	frame := wsFrame{
		Cmd:     cmdSubscribe,
		Headers: map[string]string{"req_id": reqID},
		Body:    authBody,
	}

	if err := c.writeJSON(frame); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	// Read auth response
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("connection closed")
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{}) // clear deadline
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	var resp wsFrame
	if err := json.Unmarshal(msg, &resp); err != nil {
		return fmt.Errorf("unmarshal auth response: %w", err)
	}

	if resp.ErrCode != 0 {
		return fmt.Errorf("auth failed: code=%d msg=%s", resp.ErrCode, resp.ErrMsg)
	}

	return nil
}

func (c *LongConnClient) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reqID := fmt.Sprintf("%s_%d", cmdPing, time.Now().UnixNano())
			frame := wsFrame{
				Cmd:     cmdPing,
				Headers: map[string]string{"req_id": reqID},
			}
			if err := c.writeJSON(frame); err != nil {
				logger.Warnf(ctx, "[WeCom] Heartbeat failed: %v", err)
				return
			}
		}
	}
}

func (c *LongConnClient) handleCallback(ctx context.Context, frame wsFrame) {
	var msg botMessage
	if err := json.Unmarshal(frame.Body, &msg); err != nil {
		logger.Warnf(ctx, "[WeCom] Failed to unmarshal callback body: %v", err)
		return
	}

	// Only handle text messages for now
	if msg.MsgType != "text" {
		logger.Infof(ctx, "[WeCom] Ignoring non-text message type: %s", msg.MsgType)
		return
	}

	chatType := im.ChatTypeDirect
	chatID := ""
	if msg.ChatType == "group" {
		chatType = im.ChatTypeGroup
		chatID = msg.ChatID
	}

	// Preserve req_id in Extra for reply routing
	reqID := ""
	if frame.Headers != nil {
		reqID = frame.Headers["req_id"]
	}

	incoming := &im.IncomingMessage{
		Platform:  im.PlatformWeCom,
		UserID:    msg.From.UserID,
		UserName:  msg.From.UserID,
		ChatID:    chatID,
		ChatType:  chatType,
		Content:   strings.TrimSpace(msg.Text.Content),
		MessageID: msg.MsgID,
		Extra:     map[string]string{"req_id": reqID},
	}

	if err := c.handler(ctx, incoming); err != nil {
		logger.Errorf(ctx, "[WeCom] Handle message error: %v", err)
	}
}

func (c *LongConnClient) writeJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteJSON(v)
}

func reconnectDelay(attempt int) time.Duration {
	delay := defaultReconnectBaseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
	if delay > defaultReconnectMaxDelay {
		delay = defaultReconnectMaxDelay
	}
	return delay
}
