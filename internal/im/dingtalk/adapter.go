package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// httpClient is a shared HTTP client with a reasonable timeout for DingTalk API calls.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// Compile-time checks.
var (
	_ im.Adapter      = (*Adapter)(nil)
	_ im.StreamSender = (*Adapter)(nil)
)

// Adapter implements im.Adapter for DingTalk.
type Adapter struct {
	clientID     string
	clientSecret string
	client       *LongConnClient // nil for webhook mode

	// accessToken cache
	tokenMu    sync.RWMutex
	token      string
	tokenExpAt time.Time
}

// NewWebhookAdapter creates a DingTalk adapter for HTTP callback mode.
func NewWebhookAdapter(clientID, clientSecret string) *Adapter {
	return &Adapter{
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// NewAdapter creates a DingTalk adapter backed by a stream client.
func NewAdapter(client *LongConnClient, clientID, clientSecret string) *Adapter {
	return &Adapter{
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       client,
	}
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformDingtalk
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	return false // DingTalk does not use URL verification challenges.
}

// VerifyCallback verifies the DingTalk webhook signature (HmacSHA256).
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.clientSecret == "" {
		return nil
	}

	timestamp := c.GetHeader("Timestamp")
	sign := c.GetHeader("Sign")
	if timestamp == "" || sign == "" {
		return fmt.Errorf("missing timestamp or sign header")
	}

	// Check timestamp is within 1 hour
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	diff := time.Now().UnixMilli() - ts
	if diff > 3600*1000 || diff < -3600*1000 {
		return fmt.Errorf("timestamp expired")
	}

	// Verify HMAC-SHA256 signature
	stringToSign := timestamp + "\n" + a.clientSecret
	h := hmac.New(sha256.New, []byte(a.clientSecret))
	h.Write([]byte(stringToSign))
	expectedSign := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(sign), []byte(expectedSign)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// DingTalk callback message structure.
type callbackMessage struct {
	ConversationID   string           `json:"conversationId"`
	ConversationType string           `json:"conversationType"` // "1" = single, "2" = group
	MsgID            string           `json:"msgId"`
	Msgtype          string           `json:"msgtype"` // "text", "picture", "richText", "file"
	Text             *textContent     `json:"text"`
	SenderNick       string           `json:"senderNick"`
	SenderStaffId    string           `json:"senderStaffId"`
	SenderID         string           `json:"senderId"`
	SessionWebhook   string           `json:"sessionWebhook"`
	RobotCode        string           `json:"robotCode"`
	AtUsers          []atUser         `json:"atUsers"`
	IsInAtList       bool             `json:"isInAtList"`
	ChatbotCorpId    string           `json:"chatbotCorpId"`
}

type textContent struct {
	Content string `json:"content"`
}

type atUser struct {
	DingtalkID string `json:"dingtalkId"`
	StaffID    string `json:"staffId"`
}

func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var msg callbackMessage
	if err := json.Unmarshal(bodyBytes, &msg); err != nil {
		return nil, fmt.Errorf("parse callback: %w", err)
	}

	return parseCallbackMessage(&msg), nil
}

func parseCallbackMessage(msg *callbackMessage) *im.IncomingMessage {
	chatType := im.ChatTypeDirect
	chatID := ""
	if msg.ConversationType == "2" {
		chatType = im.ChatTypeGroup
		chatID = msg.ConversationID
	}

	userID := msg.SenderStaffId
	if userID == "" {
		userID = msg.SenderID
	}

	content := ""
	if msg.Text != nil {
		content = strings.TrimSpace(msg.Text.Content)
	}

	incoming := &im.IncomingMessage{
		Platform:    im.PlatformDingtalk,
		UserID:      userID,
		UserName:    msg.SenderNick,
		ChatID:      chatID,
		ChatType:    chatType,
		MessageID:   msg.MsgID,
		MessageType: im.MessageTypeText,
		Content:     content,
		Extra: map[string]string{
			"session_webhook": msg.SessionWebhook,
		},
	}

	return incoming
}

// ── Send reply ──

func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	// Prefer sessionWebhook for simplicity
	sessionWebhook := ""
	if incoming.Extra != nil {
		sessionWebhook = incoming.Extra["session_webhook"]
	}

	if sessionWebhook != "" {
		return a.replyViaSessionWebhook(ctx, sessionWebhook, reply.Content)
	}

	// Fallback to OpenAPI
	return a.replyViaOpenAPI(ctx, incoming, reply.Content)
}

func (a *Adapter) replyViaSessionWebhook(ctx context.Context, webhookURL, content string) error {
	body := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": "Reply",
			"text":  content,
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dingtalk sessionWebhook returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (a *Adapter) replyViaOpenAPI(ctx context.Context, incoming *im.IncomingMessage, content string) error {
	token, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	msgParam, err := json.Marshal(map[string]string{"title": "Reply", "text": content})
	if err != nil {
		return fmt.Errorf("marshal msgParam: %w", err)
	}

	var apiURL string
	body := map[string]interface{}{
		"robotCode": a.clientID,
		"msgKey":    "sampleMarkdown",
		"msgParam":  string(msgParam),
	}

	if incoming.ChatType == im.ChatTypeGroup {
		apiURL = "https://api.dingtalk.com/v1.0/robot/groupMessages/send"
		body["openConversationId"] = incoming.ChatID
	} else {
		apiURL = "https://api.dingtalk.com/v1.0/robot/oToMessages/batchSend"
		body["userIds"] = []string{incoming.UserID}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dingtalk OpenAPI returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// getAccessToken returns a cached or fresh DingTalk access token.
func (a *Adapter) getAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.RLock()
	if a.token != "" && time.Now().Before(a.tokenExpAt) {
		token := a.token
		a.tokenMu.RUnlock()
		return token, nil
	}
	a.tokenMu.RUnlock()

	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	// Double check after acquiring write lock
	if a.token != "" && time.Now().Before(a.tokenExpAt) {
		return a.token, nil
	}

	body := map[string]string{
		"appKey":    a.clientID,
		"appSecret": a.clientSecret,
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.dingtalk.com/v1.0/oauth2/accessToken",
		bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("dingtalk accessToken returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int64  `json:"expireIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token from dingtalk")
	}

	a.token = result.AccessToken
	// Refresh 5 minutes before expiry
	a.tokenExpAt = time.Now().Add(time.Duration(result.ExpireIn)*time.Second - 5*time.Minute)

	return a.token, nil
}

// ── StreamSender implementation ──
// DingTalk does not support editing messages in place. We accumulate content
// and send the full reply at EndStream via sessionWebhook.

type streamState struct {
	mu             sync.Mutex
	content        strings.Builder
	sessionWebhook string
}

var (
	streamsMu sync.Mutex
	dStreams   = map[string]*streamState{}
)

func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	sessionWebhook := ""
	if incoming.Extra != nil {
		sessionWebhook = incoming.Extra["session_webhook"]
	}

	streamID := fmt.Sprintf("dt:%s:%s", incoming.UserID, incoming.MessageID)

	streamsMu.Lock()
	dStreams[streamID] = &streamState{
		sessionWebhook: sessionWebhook,
	}
	streamsMu.Unlock()

	logger.Infof(ctx, "[DingTalk] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

func (a *Adapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	if content == "" {
		return nil
	}

	streamsMu.Lock()
	state, ok := dStreams[streamID]
	streamsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	state.content.WriteString(content)
	state.mu.Unlock()

	return nil
}

func (a *Adapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	streamsMu.Lock()
	state, ok := dStreams[streamID]
	delete(dStreams, streamID)
	streamsMu.Unlock()

	if !ok {
		return nil
	}

	state.mu.Lock()
	fullContent := state.content.String()
	state.mu.Unlock()

	if state.sessionWebhook != "" {
		if err := a.replyViaSessionWebhook(ctx, state.sessionWebhook, fullContent); err != nil {
			logger.Warnf(ctx, "[DingTalk] Failed to end stream: %v", err)
		}
	} else {
		// Fallback to OpenAPI
		if err := a.replyViaOpenAPI(ctx, incoming, fullContent); err != nil {
			logger.Warnf(ctx, "[DingTalk] Failed to end stream via OpenAPI: %v", err)
		}
	}

	logger.Infof(ctx, "[DingTalk] Streaming ended: stream_id=%s", streamID)
	return nil
}
