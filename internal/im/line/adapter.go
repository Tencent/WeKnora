// Package line implements the LINE Messaging API adapter for WeKnora IM.
//
// LINE delivers webhook events with a short-lived reply token (30 seconds).
// RAG answers frequently take longer, so SendReply tries the reply API first
// and falls back to the push API when the token has already expired.
package line

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
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// httpClient is a shared HTTP client with a reasonable timeout for LINE API calls.
var httpClient = secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
	Timeout:      15 * time.Second,
	MaxRedirects: 5,
})

// apiBaseURL is the LINE Messaging API host. Overridable in tests.
var apiBaseURL = "https://api.line.me"

// maxTextLength is the LINE text message limit (5,000 characters).
const maxTextLength = 5000

// Compile-time check.
var _ im.Adapter = (*Adapter)(nil)

// Adapter implements im.Adapter for LINE Official Accounts (Messaging API).
type Adapter struct {
	channelSecret      string
	channelAccessToken string
}

// NewAdapter creates a LINE adapter.
func NewAdapter(channelSecret, channelAccessToken string) *Adapter {
	return &Adapter{
		channelSecret:      channelSecret,
		channelAccessToken: channelAccessToken,
	}
}

// Platform returns the LINE platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformLine
}

// HandleURLVerification reports false: LINE has no challenge handshake, so the
// normal callback path handles every request.
func (a *Adapter) HandleURLVerification(*gin.Context) bool {
	return false
}

// VerifyCallback checks X-Line-Signature, which is base64(HMAC-SHA256(body)).
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.channelSecret == "" {
		return fmt.Errorf("LINE channel secret is required")
	}

	im.LimitWebhookBody(c)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	signature := c.GetHeader("X-Line-Signature")
	if signature == "" {
		return fmt.Errorf("missing X-Line-Signature header")
	}

	mac := hmac.New(sha256.New, []byte(a.channelSecret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("invalid LINE signature")
	}
	return nil
}

// webhookPayload is the top-level LINE webhook body.
type webhookPayload struct {
	Destination string         `json:"destination"`
	Events      []webhookEvent `json:"events"`
}

type webhookEvent struct {
	Type           string       `json:"type"`
	ReplyToken     string       `json:"replyToken"`
	Timestamp      int64        `json:"timestamp"`
	Source         eventSource  `json:"source"`
	Message        eventMessage `json:"message"`
	WebhookEventID string       `json:"webhookEventId"`
}

type eventSource struct {
	Type    string `json:"type"` // user | group | room
	UserID  string `json:"userId"`
	GroupID string `json:"groupId"`
	RoomID  string `json:"roomId"`
}

type eventMessage struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseCallback converts a LINE webhook into an IncomingMessage.
// Returns (nil, nil) for events that carry no user text.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	im.LimitWebhookBody(c)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode LINE payload: %w", err)
	}

	for _, ev := range payload.Events {
		if ev.Type != "message" {
			continue
		}
		if ev.Message.Type != "text" {
			logger.Infof(context.Background(),
				"[LINE] ignoring unsupported message type %q", ev.Message.Type)
			continue
		}

		msg := &im.IncomingMessage{
			Platform:    im.PlatformLine,
			MessageType: im.MessageTypeText,
			UserID:      ev.Source.UserID,
			Content:     ev.Message.Text,
			MessageID:   ev.Message.ID, // dedup key: the IM service drops repeats within 5 min
			ReplyToken:  ev.ReplyToken,
			ChatType:    im.ChatTypeDirect,
		}

		switch ev.Source.Type {
		case "group":
			msg.ChatType = im.ChatTypeGroup
			msg.ChatID = ev.Source.GroupID
		case "room":
			msg.ChatType = im.ChatTypeGroup
			msg.ChatID = ev.Source.RoomID
		}

		return msg, nil
	}

	return nil, nil
}

// SendReply tries the reply API (free, but only valid ~30s) and falls back to
// the push API when the token has expired or the reply fails.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	text := clampText(reply.Content)
	if text == "" {
		return nil
	}

	if incoming.ReplyToken != "" {
		err := a.post(ctx, "/v2/bot/message/reply", map[string]any{
			"replyToken": incoming.ReplyToken,
			"messages":   textMessages(text),
		})
		if err == nil {
			return nil
		}
		logger.Warnf(ctx, "[LINE] reply failed, falling back to push: %v", err)
	}

	to := incoming.ChatID
	if to == "" {
		to = incoming.UserID
	}
	if to == "" {
		return fmt.Errorf("LINE: no recipient (empty userId and chatId)")
	}

	return a.post(ctx, "/v2/bot/message/push", map[string]any{
		"to":       to,
		"messages": textMessages(text),
	})
}

func textMessages(text string) []map[string]string {
	return []map[string]string{{"type": "text", "text": text}}
}

func clampText(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxTextLength {
		return string(r)
	}
	return string(r[:maxTextLength-1]) + "…"
}

func (a *Adapter) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.channelAccessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call LINE API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("LINE API %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
}
