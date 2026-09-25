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
	webSocketMaxConnectionAge  = 6 * time.Hour
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
	message  *callbackMessage
	ack      []byte
	cmd      string
	typeName string
	event    string
	seq      int64
	hasSeq   bool
	needAck  bool
	control  string
}

type LongConnClient struct {
	channelID        string
	url              string
	handler          func(context.Context, *im.IncomingMessage) error
	dialer           *ws.Dialer
	messages         chan *im.IncomingMessage
	maxConnectionAge time.Duration

	mu                    sync.Mutex
	conn                  *ws.Conn
	closed                atomic.Bool
	connectedAt           atomic.Int64
	lastFrameAt           atomic.Int64
	lastPongAt            atomic.Int64
	lastBusinessMessageAt atomic.Int64
	reconnectCount        atomic.Int64
}

func NewLongConnClient(
	channelID, webSocketURL string,
	handler func(context.Context, *im.IncomingMessage) error,
) *LongConnClient {
	dialer := *ws.DefaultDialer
	dialer.HandshakeTimeout = webSocketHandshakeTimeout
	dialer.Proxy = nil
	dialer.NetDialContext = safeDialContext
	return &LongConnClient{
		channelID:        channelID,
		url:              webSocketURL,
		handler:          handler,
		dialer:           &dialer,
		messages:         make(chan *im.IncomingMessage, webSocketMessageQueueSize),
		maxConnectionAge: webSocketMaxConnectionAge,
	}
}

func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] Yunzhijia WebSocket connecting channel_id=%s", c.channelID)
	go c.handleMessages(ctx)
	attempt := 0
	for {
		if ctx.Err() != nil || c.closed.Load() {
			return nil
		}

		startedAt := time.Now()
		err := c.connectAndRun(ctx)
		if ctx.Err() != nil || c.closed.Load() {
			return nil
		}
		if time.Since(startedAt) >= webSocketReconnectDelays[len(webSocketReconnectDelays)-1] {
			attempt = 0
		}

		delay := webSocketReconnectDelay(attempt)
		attempt++
		reconnectCount := c.reconnectCount.Add(1)
		logger.Warnf(ctx,
			"[Yunzhijia] WebSocket connection lost channel_id=%s reconnect_count=%d reason=%v; reconnecting in %v; %s",
			c.channelID, reconnectCount, err, delay, c.healthStatus(),
		)
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
		c.lastPongAt.Store(time.Now().UnixNano())
		return conn.SetReadDeadline(time.Now().Add(webSocketReadTimeout))
	})

	now := time.Now()
	c.connectedAt.Store(now.UnixNano())
	c.lastFrameAt.Store(0)
	c.lastPongAt.Store(0)
	c.lastBusinessMessageAt.Store(0)
	logger.Infof(ctx,
		"[IM] Yunzhijia WebSocket connected channel_id=%s connected_at=%s",
		c.channelID, now.Format(time.RFC3339Nano),
	)

	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	maxAgeReached := make(chan struct{})
	go c.heartbeatLoop(connectionCtx, conn)
	go c.maxConnectionAgeLoop(connectionCtx, conn, maxAgeReached)
	invalidFrames := 0
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-maxAgeReached:
				return fmt.Errorf("websocket maximum connection age %s exceeded", c.maxConnectionAge)
			default:
			}
			return fmt.Errorf("read websocket message: %w", err)
		}
		now := time.Now()
		c.lastFrameAt.Store(now.UnixNano())
		_ = conn.SetReadDeadline(now.Add(webSocketReadTimeout))

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
			logger.Warnf(ctx, "[Yunzhijia] Invalid WebSocket frame channel_id=%s: %v", c.channelID, err)
			if invalidFrames >= webSocketMaxInvalidFrames {
				return fmt.Errorf("too many invalid websocket frames")
			}
			continue
		}
		invalidFrames = 0
		c.logFrame(ctx, frame)

		if len(frame.ack) > 0 {
			if err := c.writeText(conn, frame.ack); err != nil {
				return fmt.Errorf("send websocket ack: %w", err)
			}
		}
		if frame.message == nil {
			continue
		}
		c.lastBusinessMessageAt.Store(time.Now().UnixNano())

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

func (c *LongConnClient) maxConnectionAgeLoop(ctx context.Context, conn *ws.Conn, maxAgeReached chan<- struct{}) {
	timer := time.NewTimer(c.maxConnectionAge)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		close(maxAgeReached)
		logger.Infof(ctx,
			"[Yunzhijia] WebSocket rotating after maximum connection age channel_id=%s max_connection_age=%s %s",
			c.channelID, c.maxConnectionAge, c.healthStatus(),
		)
		_ = conn.Close()
	}
}

func (c *LongConnClient) logFrame(ctx context.Context, frame *webSocketFrame) {
	logger.Debugf(ctx,
		"[Yunzhijia] WebSocket frame channel_id=%s cmd=%s type=%s event=%s seq=%d has_seq=%t need_ack=%t has_message=%t",
		c.channelID, frame.cmd, frame.typeName, frame.event, frame.seq, frame.hasSeq, frame.needAck, frame.message != nil,
	)
}

func (c *LongConnClient) healthStatus() string {
	return fmt.Sprintf(
		"connected_at=%s last_frame_at=%s last_pong_at=%s last_business_message_at=%s",
		c.healthTime(c.connectedAt.Load()),
		c.healthTime(c.lastFrameAt.Load()),
		c.healthTime(c.lastPongAt.Load()),
		c.healthTime(c.lastBusinessMessageAt.Load()),
	)
}

func (c *LongConnClient) healthTime(unixNano int64) string {
	if unixNano == 0 {
		return ""
	}
	return time.Unix(0, unixNano).UTC().Format(time.RFC3339Nano)
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
				logger.Warnf(ctx,
					"[Yunzhijia] WebSocket heartbeat failed channel_id=%s reason=%v; %s",
					c.channelID, err, c.healthStatus(),
				)
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

	typeName := rawString(fields["type"])
	cmd := strings.ToLower(strings.TrimSpace(rawString(fields["cmd"])))
	event := strings.ToLower(strings.TrimSpace(rawString(fields["event"])))
	frame := &webSocketFrame{cmd: cmd, typeName: typeName, event: event}
	_ = json.Unmarshal(fields["needAck"], &frame.needAck)
	frame.hasSeq = json.Unmarshal(fields["seq"], &frame.seq) == nil
	if msg := decodeBusinessMessage(trimmed); msg != nil {
		frame.message = msg
		return frame, nil
	}

	if strings.EqualFold(typeName, "robotMessage") {
		if msg := decodeBusinessMessage(fields["msg"]); msg != nil {
			frame.message = msg
			return frame, nil
		}
		return nil, fmt.Errorf("robotMessage envelope has invalid msg")
	}

	typeName = strings.ToLower(strings.TrimSpace(typeName))
	frame.typeName = typeName
	control := cmd
	if control == "" {
		control = typeName
	}
	if control == "" {
		control = event
	}

	frame.control = control
	if cmd == "directpush" || typeName == "msgchg" {
		if frame.needAck && frame.hasSeq {
			frame.ack, _ = json.Marshal(struct {
				Cmd string `json:"cmd"`
				Seq int64  `json:"seq"`
			}{Cmd: "ack", Seq: frame.seq})
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
