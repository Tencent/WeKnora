package yunzhijia

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// textMessageType is the Yunzhijia message type value for plain text messages.
const textMessageType = 2

// Compile-time check.
var _ im.Adapter = (*Adapter)(nil)

// Adapter implements im.Adapter for Yunzhijia (云之家).
type Adapter struct {
	sendMsgURL               string
	secret                   string
	httpClient               *http.Client
	allowedWebhookHostSuffix string
}

// NewAdapter creates a Yunzhijia adapter.
func NewAdapter(sendMsgURL, secret string, timeoutSeconds int, allowedHostSuffix string) *Adapter {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeDialContext
	return &Adapter{
		sendMsgURL:               strings.TrimSpace(sendMsgURL),
		secret:                   secret,
		allowedWebhookHostSuffix: strings.TrimSpace(allowedHostSuffix),
		httpClient: &http.Client{
			Timeout:   time.Duration(timeoutSeconds) * time.Second,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformYunzhijia
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

// VerifyCallback verifies the Yunzhijia callback signature (HmacSHA1).
// If secret is not configured, verification is skipped.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.secret == "" {
		return nil
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var msg callbackMessage
	if err := json.Unmarshal(bodyBytes, &msg); err != nil {
		return fmt.Errorf("parse callback for verification: %w", err)
	}

	// Read the sign header (case-insensitive via gin's GetHeader).
	sign := c.GetHeader("sign")
	if sign == "" {
		sign = c.GetHeader("Sign")
	}
	if sign == "" {
		sign = c.GetHeader("SIGN")
	}
	if sign == "" {
		return fmt.Errorf("missing sign header")
	}

	expected := computeSignature(a.secret, &msg)
	if !hmac.Equal([]byte(sign), []byte(expected)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// ParseCallback parses a Yunzhijia webhook callback into an IncomingMessage.
// Returns nil for non-text messages or empty content.
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

	return toIncomingMessage(c.Request.Context(), &msg), nil
}

func toIncomingMessage(ctx context.Context, msg *callbackMessage) *im.IncomingMessage {
	if msg.Type != textMessageType {
		logger.Infof(ctx,
			"[Yunzhijia] Skip non-text message: type=%d msgId=%s", msg.Type, msg.MsgID)
		return nil
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" {
		logger.Infof(ctx,
			"[Yunzhijia] Skip empty content: msgId=%s", msg.MsgID)
		return nil
	}

	// Conversation bots should only receive messages explicitly addressed to them.
	var mentioned bool
	content, mentioned = cleanAtMention(content, msg.RobotName)
	if !mentioned {
		logger.Infof(ctx, "[Yunzhijia] Skip message without robot mention: msgId=%s", msg.MsgID)
		return nil
	}

	if content == "" {
		logger.Infof(ctx,
			"[Yunzhijia] Skip after cleaning @mention: msgId=%s", msg.MsgID)
		return nil
	}

	chatType := im.ChatTypeGroup
	chatID := msg.RobotID

	return &im.IncomingMessage{
		Platform:    im.PlatformYunzhijia,
		MessageType: im.MessageTypeText,
		UserID:      msg.OperatorOpenid,
		UserName:    msg.OperatorName,
		ChatID:      chatID,
		ChatType:    chatType,
		Content:     content,
		MessageID:   msg.MsgID,
		Extra: map[string]string{
			"robot_id":      msg.RobotID,
			"robot_name":    msg.RobotName,
			"group_type":    fmt.Sprintf("%d", msg.GroupType),
			"operator_name": msg.OperatorName,
			"time":          fmt.Sprintf("%d", msg.Time),
		},
	}
}

// cleanAtMention removes @RobotName from the beginning of user content.
func cleanAtMention(content, robotName string) (string, bool) {
	if robotName == "" {
		return content, false
	}
	prefix := "@" + robotName
	trimmed := strings.TrimLeft(content, " \t")
	if !strings.HasPrefix(trimmed, prefix) {
		return content, false
	}
	rest := trimmed[len(prefix):]
	if rest == "" {
		return "", true
	}
	separator, _ := utf8.DecodeRuneInString(rest)
	if !unicode.IsSpace(separator) && !strings.ContainsRune(":：,，", separator) {
		return content, false
	}
	return strings.TrimLeftFunc(rest, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(":：,，", r)
	}), true
}

// SendReply sends a reply to Yunzhijia via the configured sendMsgUrl.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	if a.sendMsgURL == "" {
		return fmt.Errorf("yunzhijia send_msg_url is not configured")
	}

	// Validate the send URL to prevent SSRF.
	if err := a.validateSendURL(); err != nil {
		return err
	}

	payload := sendMessagePayload{
		MsgType: textMessageType,
		Content: reply.Content,
	}

	// When groupType == 3, don't set notifyParams (per reference implementation).
	groupType := ""
	if incoming.Extra != nil {
		groupType = incoming.Extra["group_type"]
	}
	if groupType != "3" && incoming.UserID != "" {
		payload.NotifyParams = []notifyParam{
			{
				Type:   "openIds",
				Values: []string{incoming.UserID},
			},
		}
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.sendMsgURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("yunzhijia sendMsgUrl returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// validateSendURL checks that sendMsgUrl is safe to call (HTTPS, no internal IPs, allowed host).
func (a *Adapter) validateSendURL() error {
	_, err := validateEndpointURL(a.sendMsgURL, "https", a.allowedWebhookHostSuffix)
	if err != nil {
		return fmt.Errorf("invalid send_msg_url: %w", err)
	}
	return nil
}
