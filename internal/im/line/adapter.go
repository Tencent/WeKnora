package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Adapter implements im.Adapter for the LINE Messaging API (webhook mode).
type Adapter struct {
	channelAccessToken string
	channelSecret      string
}

// Compile-time check that Adapter satisfies the im.Adapter interface.
var _ im.Adapter = (*Adapter)(nil)

// NewWebhookAdapter creates a LINE adapter for webhook delivery.
//
// channelAccessToken is the bot messaging token; channelSecret is the channel
// secret used to verify incoming webhook signatures (x-line-signature).
func NewWebhookAdapter(channelAccessToken, channelSecret string) *Adapter {
	return &Adapter{
		channelAccessToken: channelAccessToken,
		channelSecret:      channelSecret,
	}
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformLine
}

// verifySignature computes the expected LINE webhook signature: Base64-encoded
// HMAC-SHA256 of the raw request body keyed by the channel secret.
func verifySignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCallback verifies the incoming webhook signature (x-line-signature).
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.channelSecret == "" {
		return fmt.Errorf("line channel secret is required")
	}
	if a.channelAccessToken == "" {
		return fmt.Errorf("line channel access token is required")
	}

	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	expected := verifySignature(a.channelSecret, bodyBytes)
	provided := c.GetHeader("X-Line-Signature")
	if provided == "" {
		return fmt.Errorf("missing x-line-signature header")
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return fmt.Errorf("line signature verification failed")
	}
	return nil
}

// parseCallback decodes the LINE webhook payload.
func parseCallback(bodyBytes []byte) (*webhookPayload, error) {
	var payload webhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// ParseCallback decodes the LINE webhook payload into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	payload, err := parseCallback(bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse line payload: %w", err)
	}

	for i := range payload.Events {
		ev := payload.Events[i]
		if ev.Type != "message" || ev.Message == nil {
			// Non-message events (follow/unfollow/left) are handled by the
			// platform dashboard; return nil to skip without error.
			continue
		}
		if ev.Message.Type != "Text" {
			// Text-only in this phase; other message kinds are skipped.
			return nil, nil
		}
		incoming := buildIncoming(ev, ev.Message)
		incoming.Extra = map[string]string{
			"replyToken": ev.ReplyToken,
		}
		return incoming, nil
	}
	return nil, nil
}

// buildIncoming maps a single LINE message event to the unified model.
func buildIncoming(ev webhookEvent, msg *webhookMessage) *im.IncomingMessage {
	chatType := im.ChatTypeDirect
	chatID := ""
	switch ev.Source.Type {
	case "group":
		chatType = im.ChatTypeGroup
		chatID = ev.Source.GroupID
	case "room":
		chatType = im.ChatTypeGroup
		chatID = ev.Source.RoomID
	}

	return &im.IncomingMessage{
		Platform:  im.PlatformLine,
		ChatType:  chatType,
		UserID:    ev.Source.UserID,
		ChatID:    chatID,
		MessageID: msg.ID,
		Content:   strings.TrimSpace(msg.Text),
	}
}

// resolveTarget returns the push target (chat id or user id) for fallback send.
func resolveTarget(incoming *im.IncomingMessage) string {
	if incoming.ChatID != "" {
		return incoming.ChatID
	}
	return incoming.UserID
}

// sendReply delivers a reply via the reply token.
func (a *Adapter) sendReply(ctx context.Context, replyToken string, messages []map[string]interface{}) error {
	body := map[string]interface{}{
		"replyToken": replyToken,
		"messages":   messages,
	}
	return a.callAPI(ctx, "https://api.line.me/v2/bot/message/reply", body)
}

// sendPush delivers a message via the push API (fallback when the reply token
// is expired or unavailable).
func (a *Adapter) sendPush(ctx context.Context, target string, messages []map[string]interface{}) error {
	body := map[string]interface{}{
		"to":       target,
		"messages": messages,
	}
	return a.callAPI(ctx, "https://api.line.me/v2/bot/message/push", body)
}

// SendReply sends a reply back to the LINE platform. When the incoming request
// carried a reply token it is used; otherwise the call degrades to push.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	if incoming == nil {
		return fmt.Errorf("line incoming message is required")
	}

	text := strings.TrimSpace(im.FormatIMDisplayContent(reply.Content, im.StreamDisplayFinal))
	if text == "" {
		return nil
	}

	messages := []map[string]interface{}{
		{
			"type": "text",
			"text": text,
		},
	}

	if replyToken := incoming.Extra["replyToken"]; replyToken != "" {
		return a.sendReply(ctx, replyToken, messages)
	}

	// replyToken expired or absent: degrade to push to the original sender.
	target := resolveTarget(incoming)
	if target == "" {
		return fmt.Errorf("line: no target user or chat id for push reply")
	}
	logger.Warnf(ctx, "[IM/Line] reply token unavailable, falling back to push to %s", target)
	return a.sendPush(ctx, target, messages)
}

// HandleURLVerification handles LINE webhook verification / health-check
// requests (requests without a populated events array).
func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// A request without well-formed events is treated as a verification or
	// health check rather than a message event.
	payload, err := parseCallback(bodyBytes)
	if err != nil || len(payload.Events) == 0 {
		c.JSON(http.StatusOK, gin.H{"handled": "line-webhook-verification"})
		return true
	}
	return false
}

// callAPI posts a JSON body to the LINE Messaging API.
func (a *Adapter) callAPI(ctx context.Context, url string, body interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal line body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create line request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.channelAccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("line api request: %w", err)
	}
	defer resp.Body.Close()

	var apiResp struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("decode line response: %w", err)
	}
	if !apiResp.OK {
		return fmt.Errorf("line api %s failed: %s", url, string(apiResp.Result))
	}
	return nil
}

// httpClient is a shared HTTP client with an SSRF guard and a bounded timeout
// for LINE API calls. It mirrors the pattern used by other IM adapters.
var httpClient = secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
	Timeout:      15 * time.Second,
	MaxRedirects: 3,
})

// webhookPayload is the top-level LINE webhook envelope.
type webhookPayload struct {
	Destination string          `json:"destination"`
	Events      []webhookEvent  `json:"events"`
}

// webhookEvent is a single incoming LINE event.
type webhookEvent struct {
	Type       string        `json:"type"`
	Token      string        `json:"token"`
	Timestamp  string        `json:"timestamp"`
	Source     webhookSource `json:"source"`
	ReplyToken string        `json:"replyToken"`
	Message    *webhookMessage `json:"message"`
}

// webhookSource identifies the origin of an event.
type webhookSource struct {
	Type    string `json:"type"`
	UserID  string `json:"userId"`
	GroupID string `json:"groupId"`
	RoomID  string `json:"roomId"`
}

// webhookMessage is a message payload (text subset for this phase).
type webhookMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
}
