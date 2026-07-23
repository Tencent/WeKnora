package yunzhijia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	ws "github.com/gorilla/websocket"
)

const (
	webSocketHeartbeatInterval = 15 * time.Second
	webSocketReadTimeout       = 45 * time.Second
	webSocketHandshakeTimeout  = 10 * time.Second
	webSocketMaxMessageSize    = 1 << 20
	webSocketMaxInvalidFrames  = 3
	webSocketMessageQueueSize  = 64
)

var webSocketReconnectDelays = [...]time.Duration{
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

type webSocketFrame struct {
	message *callbackMessage
	ack     []byte
	control string
}

type LongConnClient struct {
	url      string
	handler  func(context.Context, *im.IncomingMessage) error
	dialer   *ws.Dialer
	messages chan *im.IncomingMessage

	mu     sync.Mutex
	conn   *ws.Conn
	closed atomic.Bool
}

func NewLongConnClient(webSocketURL string, handler func(context.Context, *im.IncomingMessage) error) *LongConnClient {
	dialer := *ws.DefaultDialer
	dialer.HandshakeTimeout = webSocketHandshakeTimeout
	dialer.Proxy = nil
	dialer.NetDialContext = safeDialContext
	return &LongConnClient{
		url:      webSocketURL,
		handler:  handler,
		dialer:   &dialer,
		messages: make(chan *im.IncomingMessage, webSocketMessageQueueSize),
	}
}

func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] Yunzhijia WebSocket connecting")
	go c.handleMessages(ctx)
	attempt := 0
	for {
		if ctx.Err() != nil || c.closed.Load() {
			return nil
		}

		connectedAt := time.Now()
		err := c.connectAndRun(ctx)
		if ctx.Err() != nil || c.closed.Load() {
			return nil
		}
		if time.Since(connectedAt) >= webSocketReconnectDelays[len(webSocketReconnectDelays)-1] {
			attempt = 0
		}

		delay := webSocketReconnectDelay(attempt)
		attempt++
		logger.Warnf(ctx, "[Yunzhijia] WebSocket connection lost: %v; reconnecting in %v", err, delay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (c *LongConnClient) Stop() {
	c.closed.Store(true)
	c.closeConn()
}

func (c *LongConnClient) connectAndRun(ctx context.Context) error {
	conn, _, err := c.dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()

	conn.SetReadLimit(webSocketMaxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(webSocketReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(webSocketReadTimeout))
	})

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()
	go c.heartbeatLoop(heartbeatCtx, conn)

	logger.Infof(ctx, "[IM] Yunzhijia WebSocket connected")
	invalidFrames := 0
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read websocket message: %w", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(webSocketReadTimeout))

		if messageType != ws.TextMessage {
			invalidFrames++
			if invalidFrames >= webSocketMaxInvalidFrames {
				return fmt.Errorf("too many non-text websocket frames")
			}
			continue
		}

		frame, err := parseWebSocketFrame(data)
		if err != nil {
			invalidFrames++
			logger.Warnf(ctx, "[Yunzhijia] Invalid WebSocket frame: %v", err)
			if invalidFrames >= webSocketMaxInvalidFrames {
				return fmt.Errorf("too many invalid websocket frames")
			}
			continue
		}
		invalidFrames = 0

		if len(frame.ack) > 0 {
			if err := c.writeText(conn, frame.ack); err != nil {
				return fmt.Errorf("send websocket ack: %w", err)
			}
		}
		if frame.message == nil {
			continue
		}

		incoming := toIncomingMessage(ctx, frame.message)
		if incoming == nil {
			continue
		}
		select {
		case c.messages <- incoming:
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *LongConnClient) handleMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.messages:
			if err := c.handler(ctx, msg); err != nil {
				logger.Errorf(ctx, "[Yunzhijia] Handle WebSocket message failed: %v", err)
			}
		}
	}
}

func (c *LongConnClient) heartbeatLoop(ctx context.Context, conn *ws.Conn) {
	ticker := time.NewTicker(webSocketHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteControl(ws.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				logger.Warnf(ctx, "[Yunzhijia] WebSocket heartbeat failed: %v", err)
				_ = conn.Close()
				return
			}
		}
	}
}

func (c *LongConnClient) writeText(conn *ws.Conn, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != conn {
		return fmt.Errorf("websocket connection changed")
	}
	return conn.WriteMessage(ws.TextMessage, data)
}

func (c *LongConnClient) closeConn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func webSocketReconnectDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(webSocketReconnectDelays) {
		return webSocketReconnectDelays[len(webSocketReconnectDelays)-1]
	}
	return webSocketReconnectDelays[attempt]
}

func parseWebSocketFrame(data []byte) (*webSocketFrame, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty frame")
	}

	plain := strings.ToLower(string(trimmed))
	if plain == "ping" || plain == "pong" {
		return &webSocketFrame{control: plain}, nil
	}

	var stringPayload string
	if err := json.Unmarshal(trimmed, &stringPayload); err == nil {
		control := strings.ToLower(strings.TrimSpace(stringPayload))
		if control == "ping" || control == "pong" {
			return &webSocketFrame{control: control}, nil
		}
		return nil, fmt.Errorf("unknown string frame")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, fmt.Errorf("decode frame: %w", err)
	}

	if msg := decodeBusinessMessage(trimmed); msg != nil {
		return &webSocketFrame{message: msg}, nil
	}

	typeName := rawString(fields["type"])
	if strings.EqualFold(typeName, "robotMessage") {
		if msg := decodeBusinessMessage(fields["msg"]); msg != nil {
			return &webSocketFrame{message: msg}, nil
		}
		return nil, fmt.Errorf("robotMessage envelope has invalid msg")
	}

	cmd := strings.ToLower(strings.TrimSpace(rawString(fields["cmd"])))
	typeName = strings.ToLower(strings.TrimSpace(typeName))
	event := strings.ToLower(strings.TrimSpace(rawString(fields["event"])))
	control := cmd
	if control == "" {
		control = typeName
	}
	if control == "" {
		control = event
	}

	frame := &webSocketFrame{control: control}
	if cmd == "directpush" || typeName == "msgchg" {
		var needAck bool
		_ = json.Unmarshal(fields["needAck"], &needAck)
		var seq int64
		if needAck && json.Unmarshal(fields["seq"], &seq) == nil {
			frame.ack, _ = json.Marshal(struct {
				Cmd string `json:"cmd"`
				Seq int64  `json:"seq"`
			}{Cmd: "ack", Seq: seq})
		}
		return frame, nil
	}
	if control != "" {
		return frame, nil
	}
	return nil, fmt.Errorf("frame has no business message or control type")
}

func decodeBusinessMessage(data []byte) *callbackMessage {
	if len(data) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}
	for _, key := range []string{"robotId", "robotName", "operatorOpenid", "operatorName", "msgId", "content"} {
		var value string
		if json.Unmarshal(fields[key], &value) != nil {
			return nil
		}
	}
	var messageType int
	if json.Unmarshal(fields["type"], &messageType) != nil {
		return nil
	}
	var messageTime int64
	if json.Unmarshal(fields["time"], &messageTime) != nil {
		return nil
	}
	var msg callbackMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}
	return &msg
}

func rawString(raw json.RawMessage) string {
	var value string
	if len(raw) > 0 && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}
