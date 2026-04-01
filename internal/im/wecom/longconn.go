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

	// readTimeout is how long the receive loop waits for any message (including
	// heartbeat pong) before treating the connection as dead. Set to 3× heartbeat
	// interval so a single missed pong does not cause a spurious reconnect.
	readTimeout = 3 * defaultHeartbeatInterval
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
// Supports text, image, file, voice, and mixed message types.
// Reference: https://developer.work.weixin.qq.com/document/path/100719
type botMessage struct {
	MsgID      string `json:"msgid"`
	AiBotID    string `json:"aibotid"`
	ChatID     string `json:"chatid"`
	ChatType   string `json:"chattype"` // "single" or "group"
	MsgType    string `json:"msgtype"`  // "text", "image", "file", "voice", "video", "mixed", "stream"
	CreateTime int64  `json:"create_time"`
	From       struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
	Image struct {
		URL    string `json:"url"`    // encrypted download URL, valid for 5 minutes
		AESKey string `json:"aeskey"` // per-message AES key for decrypting downloaded content
	} `json:"image"`
	File struct {
		URL    string `json:"url"`    // encrypted download URL, valid for 5 minutes
		AESKey string `json:"aeskey"` // per-message AES key for decrypting downloaded content
	} `json:"file"`
	Voice struct {
		Content string `json:"content"` // speech-to-text result
	} `json:"voice"`
	Video struct {
		URL    string `json:"url"`    // encrypted download URL, valid for 5 minutes
		AESKey string `json:"aeskey"` // per-message AES key for decrypting downloaded content
	} `json:"video"`
	Mixed struct {
		MsgItem []botMixedItem `json:"msg_item"`
	} `json:"mixed"`
	Quote *botMessage `json:"quote,omitempty"` // quoted message (optional)
	Event struct {
		EventType string `json:"eventtype"`
	} `json:"event"`
}

// botMixedItem is one element in a mixed (text+image) message.
type botMixedItem struct {
	MsgType string `json:"msgtype"` // "text" or "image"
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
	Image struct {
		URL    string `json:"url"`
		AESKey string `json:"aeskey"`
	} `json:"image"`
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

	// streamBufs tracks accumulated content per stream ID.
	// WeCom stream protocol is replace-based: each frame's content replaces
	// the previously displayed text, so we must send the full accumulated text.
	streamBufsMu sync.Mutex
	streamBufs   map[string]*strings.Builder

	// botDisplayName caches the bot's display name for @mention stripping.
	// Set from credentials "bot_name", or learned from double-space messages.
	botDisplayName atomic.Value // string
}

// NewLongConnClient creates a WeCom long connection client.
// botName is the bot's display name for @mention stripping; empty to auto-detect.
func NewLongConnClient(botID, secret, botName string, handler MessageHandler) *LongConnClient {
	c := &LongConnClient{
		botID:   botID,
		secret:  secret,
		handler: handler,
	}
	if botName != "" {
		c.botDisplayName.Store(botName)
	}
	return c
}

// Start connects and runs the long connection loop. It reconnects automatically on failure.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] WeCom WebSocket connecting (bot_id=%s)...", c.botID)

	attempts := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		connectedAt := time.Now()
		err := c.connectAndRun(ctx)
		if c.closed.Load() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// If the connection was up for longer than the max backoff window,
		// the disconnect is likely transient — reset so we retry quickly.
		if time.Since(connectedAt) > defaultReconnectMaxDelay {
			attempts = 0
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
	var reqID string
	if incoming.Extra != nil {
		reqID = incoming.Extra["req_id"]
	}
	if reqID == "" {
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

// ──────────────────────────────────────────────────────────────────────
// Streaming support: send answer chunks over WebSocket in real-time
// ──────────────────────────────────────────────────────────────────────

// StartStream begins a streaming reply session.
// Returns a stream ID that must be used in subsequent chunk/end calls.
func (c *LongConnClient) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	if incoming.Extra == nil || incoming.Extra["req_id"] == "" {
		return "", fmt.Errorf("missing req_id in incoming message extra")
	}
	streamID := fmt.Sprintf("stream_%d", c.reqSeq.Add(1))

	// Initialize the accumulation buffer for this stream
	c.streamBufsMu.Lock()
	if c.streamBufs == nil {
		c.streamBufs = make(map[string]*strings.Builder)
	}
	c.streamBufs[streamID] = &strings.Builder{}
	c.streamBufsMu.Unlock()

	return streamID, nil
}

// SendStreamChunk accumulates the content and sends the full text so far.
// WeCom stream protocol is replace-based: each frame replaces the previous display.
func (c *LongConnClient) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	if content == "" {
		return nil
	}

	// Accumulate
	c.streamBufsMu.Lock()
	buf, ok := c.streamBufs[streamID]
	if !ok {
		c.streamBufsMu.Unlock()
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}
	buf.WriteString(content)
	fullContent := buf.String()
	c.streamBufsMu.Unlock()

	return c.sendStreamFrame(incoming, streamID, fullContent, false)
}

// EndStream sends the final frame with the full accumulated content and cleans up.
func (c *LongConnClient) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	c.streamBufsMu.Lock()
	buf, ok := c.streamBufs[streamID]
	var fullContent string
	if ok {
		fullContent = buf.String()
		delete(c.streamBufs, streamID)
	}
	c.streamBufsMu.Unlock()

	return c.sendStreamFrame(incoming, streamID, fullContent, true)
}

func (c *LongConnClient) sendStreamFrame(incoming *im.IncomingMessage, streamID, content string, finish bool) error {
	var reqID string
	if incoming.Extra != nil {
		reqID = incoming.Extra["req_id"]
	}
	if reqID == "" {
		return fmt.Errorf("missing req_id in incoming message extra")
	}

	body := streamReplyBody{MsgType: "stream"}
	body.Stream.ID = streamID
	body.Stream.Finish = finish
	body.Stream.Content = content

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal stream body: %w", err)
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

		// Clear in-flight stream buffers to prevent memory leaks on reconnect.
		// Streams interrupted by a connection drop cannot be resumed.
		c.streamBufsMu.Lock()
		c.streamBufs = nil
		c.streamBufsMu.Unlock()
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

	// Message receive loop with read deadline.
	// The deadline is reset on every successful read; if no message arrives
	// within readTimeout (including heartbeat pong frames), the connection
	// is considered dead and we fall through to reconnect.
	for {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
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
				logger.Warnf(ctx, "[WeCom] Heartbeat failed: %v, closing connection to trigger reconnect", err)
				c.closeConn()
				return
			}
		}
	}
}

func (c *LongConnClient) handleCallback(ctx context.Context, frame wsFrame) {
	// Log raw message body for debugging
	logger.Debugf(ctx, "[WeCom] Raw callback body: %s", string(frame.Body))

	var msg botMessage
	if err := json.Unmarshal(frame.Body, &msg); err != nil {
		logger.Warnf(ctx, "[WeCom] Failed to unmarshal callback body: %v", err)
		return
	}

	logger.Debugf(ctx, "[WeCom] Parsed message: msgid=%s msgtype=%s from=%s chattype=%s text=%q image_url=%q file_url=%q voice=%q mixed_items=%d",
		msg.MsgID, msg.MsgType, msg.From.UserID, msg.ChatType,
		msg.Text.Content, msg.Image.URL, msg.File.URL, msg.Voice.Content, len(msg.Mixed.MsgItem))

	// Handle server-side events (e.g. disconnected_event) before normal messages.
	if msg.MsgType == "event" {
		switch msg.Event.EventType {
		case "disconnected_event":
			logger.Warnf(ctx, "[WeCom] Server sent disconnected_event, closing connection to trigger reconnect")
			c.closeConn()
		default:
			logger.Infof(ctx, "[WeCom] Ignoring event type: %s", msg.Event.EventType)
		}
		return
	}

	chatType := im.ChatTypeDirect
	chatID := ""
	isGroup := msg.ChatType == "group"
	if isGroup {
		chatType = im.ChatTypeGroup
		chatID = msg.ChatID
	}

	// Preserve req_id in Extra for reply routing
	reqID := ""
	if frame.Headers != nil {
		reqID = frame.Headers["req_id"]
	}

	var incoming *im.IncomingMessage

	switch msg.MsgType {
	case "text":
		// WeCom does not strip @mention in group chat; strip it so slash
		// commands (/stop, /clear) are recognized.
		textContent := msg.Text.Content
		if isGroup {
			textContent = c.stripAtMention(textContent)
		}
		incoming = &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeText,
			UserID:      msg.From.UserID,
			UserName:    msg.From.UserID,
			ChatID:      chatID,
			ChatType:    chatType,
			Content:     strings.TrimSpace(textContent),
			MessageID:   msg.MsgID,
			Extra:       map[string]string{"req_id": reqID},
		}

	case "voice":
		// WeCom returns speech-to-text content directly — treat as text query
		if msg.Voice.Content == "" {
			logger.Infof(ctx, "[WeCom] Ignoring voice message with empty content")
			return
		}
		incoming = &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeText,
			UserID:      msg.From.UserID,
			UserName:    msg.From.UserID,
			ChatID:      chatID,
			ChatType:    chatType,
			Content:     strings.TrimSpace(msg.Voice.Content),
			MessageID:   msg.MsgID,
			Extra:       map[string]string{"req_id": reqID},
		}

	case "image":
		if msg.Image.URL == "" {
			logger.Infof(ctx, "[WeCom] Ignoring image message with empty URL")
			return
		}
		incoming = &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeImage,
			UserID:      msg.From.UserID,
			UserName:    msg.From.UserID,
			ChatID:      chatID,
			ChatType:    chatType,
			MessageID:   msg.MsgID,
			FileKey:     msg.Image.URL, // store encrypted URL in FileKey
			FileName:    msg.MsgID + ".png",
			Extra:       map[string]string{"req_id": reqID, "aes_key": msg.Image.AESKey},
		}

	case "file":
		if msg.File.URL == "" {
			logger.Infof(ctx, "[WeCom] Ignoring file message with empty URL")
			return
		}
		incoming = &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeFile,
			UserID:      msg.From.UserID,
			UserName:    msg.From.UserID,
			ChatID:      chatID,
			ChatType:    chatType,
			MessageID:   msg.MsgID,
			FileKey:     msg.File.URL, // store encrypted URL in FileKey
			FileName:    msg.MsgID,    // WeCom doesn't provide file name directly
			Extra:       map[string]string{"req_id": reqID, "aes_key": msg.File.AESKey},
		}

	case "mixed":
		// Extract text parts for QA content, and detect if any images are present
		incoming = c.convertMixedMessage(&msg, chatID, chatType, reqID)
		if incoming == nil {
			logger.Infof(ctx, "[WeCom] Ignoring empty mixed message")
			return
		}

	default:
		logger.Infof(ctx, "[WeCom] Ignoring unsupported message type: %s", msg.MsgType)
		return
	}

	// Populate quote context if the incoming message has a quoted/replied message.
	if incoming != nil && msg.Quote != nil {
		incoming.Quote = buildQuotedMessage(msg.Quote, msg.AiBotID)
		if incoming.Quote != nil {
			logger.Infof(ctx, "[WeCom] Quote detected: msgid=%s sender=%s is_bot=%v content_len=%d non_text_type=%s",
				msg.Quote.MsgID, msg.Quote.From.UserID, incoming.Quote.IsBotMessage, len(incoming.Quote.Content), incoming.Quote.NonTextType)
			// Debug: log raw IDs for bot identity verification during initial rollout
			logger.Debugf(ctx, "[WeCom] Quote identity debug: quote.from.userid=%q quote.aibotid=%q msg.aibotid=%q",
				msg.Quote.From.UserID, msg.Quote.AiBotID, msg.AiBotID)
		}
	}

	if err := c.handler(ctx, incoming); err != nil {
		logger.Errorf(ctx, "[WeCom] Handle message error: %v", err)
	}
}

// convertMixedMessage converts a WeCom mixed (text+image) message.
// Extracts all text content for QA; if there's only images, treat as image message.
func (c *LongConnClient) convertMixedMessage(msg *botMessage, chatID string, chatType im.ChatType, reqID string) *im.IncomingMessage {
	isGroup := chatType == im.ChatTypeGroup
	var textParts []string
	var firstImageURL string
	var firstImageAESKey string

	for _, item := range msg.Mixed.MsgItem {
		switch item.MsgType {
		case "text":
			t := strings.TrimSpace(item.Text.Content)
			if isGroup {
				t = c.stripAtMention(t)
			}
			if t != "" {
				textParts = append(textParts, t)
			}
		case "image":
			if firstImageURL == "" && item.Image.URL != "" {
				firstImageURL = item.Image.URL
				firstImageAESKey = item.Image.AESKey
			}
		}
	}

	// If there's text content, treat as text message (QA query)
	if len(textParts) > 0 {
		return &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeText,
			UserID:      msg.From.UserID,
			UserName:    msg.From.UserID,
			ChatID:      chatID,
			ChatType:    chatType,
			Content:     strings.Join(textParts, "\n"),
			MessageID:   msg.MsgID,
			Extra:       map[string]string{"req_id": reqID},
		}
	}

	// Only images, treat as image message (save to KB)
	if firstImageURL != "" {
		return &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeImage,
			UserID:      msg.From.UserID,
			UserName:    msg.From.UserID,
			ChatID:      chatID,
			ChatType:    chatType,
			MessageID:   msg.MsgID,
			FileKey:     firstImageURL,
			FileName:    msg.MsgID + ".png",
			Extra:       map[string]string{"req_id": reqID, "aes_key": firstImageAESKey},
		}
	}

	return nil
}

// closeConn forcibly closes the underlying WebSocket, which unblocks any
// pending ReadMessage call in the receive loop and triggers a reconnection.
func (c *LongConnClient) closeConn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// stripAtMentionBasic removes a leading "@Name" prefix from group chat content.
// Strategies: double-space split → heuristic (space + "/" or CJK) → first @word.
// Used directly by the webhook adapter (stateless) and as the base for
// LongConnClient.stripAtMention (stateful).
func stripAtMentionBasic(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "@") {
		return content
	}
	// Some WeCom clients insert two spaces between @mention and user text.
	if idx := strings.Index(content, "  "); idx > 0 {
		return strings.TrimSpace(content[idx+2:])
	}
	// Heuristic: bot names are ASCII words; user content starts with "/"
	// or non-ASCII (CJK). Scan for the transition.
	for i := 1; i < len(content); i++ {
		if content[i] == ' ' && i+1 < len(content) {
			if next := content[i+1]; next == '/' || next >= 0x80 {
				return strings.TrimSpace(content[i+1:])
			}
		}
	}
	// Fallback: strip first @word.
	if idx := strings.Index(content, " "); idx > 0 {
		return strings.TrimSpace(content[idx+1:])
	}
	return content
}

// stripAtMention removes the leading "@BotName" prefix from group chat messages.
// Bot names may contain spaces (e.g., "WeKnora Bot"), so this adds two strategies
// on top of stripAtMentionBasic: (1) double-space split with bot-name learning,
// (2) cached/configured bot name prefix match.
// Concurrent calls are safe; atomic.Value races are benign (same bot name).
func (c *LongConnClient) stripAtMention(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "@") {
		return content
	}

	// Strategy 1: double-space separator. Learn bot name on first occurrence
	// (skip if already cached to avoid false positives from user double-spaces).
	if idx := strings.Index(content, "  "); idx > 0 {
		botName := content[1:idx] // between "@" and "  "
		if cached, _ := c.botDisplayName.Load().(string); cached == "" && botName != "" {
			c.botDisplayName.Store(botName)
		}
		return strings.TrimSpace(content[idx+2:])
	}

	// Strategy 2: cached/configured bot name with word-boundary check.
	if name, _ := c.botDisplayName.Load().(string); name != "" {
		prefix := "@" + name
		if strings.HasPrefix(content, prefix) && (len(content) == len(prefix) || content[len(prefix)] == ' ') {
			return strings.TrimSpace(content[len(prefix):])
		}
	}

	// Strategy 3: delegate to stateless helper (heuristic scan + first-@word fallback).
	return stripAtMentionBasic(content)
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
