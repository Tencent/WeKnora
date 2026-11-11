// Package whatsapp implements the IM adapter for the WhatsApp Business Platform
// (Meta Cloud API).
//
// Inbound messages arrive on a Meta webhook: a GET handshake proves ownership
// of the callback URL, then every event is a POST signed with the Meta app
// secret. Replies go out through the Graph API messages endpoint.
//
// Docs: https://developers.facebook.com/docs/whatsapp/cloud-api
package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Compile-time checks. WhatsApp cannot edit a sent message, so the stream is
// not a replaceable card: StartStream shows the native typing indicator as the
// progress placeholder and FinalizeStream sends the completed answer once.
var (
	_ im.Adapter                  = (*Adapter)(nil)
	_ im.FullOutputProgressSender = (*Adapter)(nil)
)

const (
	// graphAPIBaseURL is the versioned Graph API root used for outbound calls.
	graphAPIBaseURL = "https://graph.facebook.com/v23.0"

	// maxTextLength is the WhatsApp limit for the body of a text message.
	maxTextLength = 4096

	signatureHeader = "X-Hub-Signature-256"
	signaturePrefix = "sha256="

	// typingMaxDuration bounds how long the typing indicator is kept alive.
	typingMaxDuration = 3 * time.Minute
)

// typingRefreshInterval re-sends the typing indicator before WhatsApp clears
// it on its own (after 25 seconds). A variable so tests can shorten it.
var typingRefreshInterval = 20 * time.Second

// Adapter implements im.Adapter for the WhatsApp Cloud API.
type Adapter struct {
	accessToken   string
	phoneNumberID string
	appSecret     string // signs inbound webhook bodies
	verifyToken   string // echoed by Meta during the GET handshake
	apiBaseURL    string
	httpClient    *http.Client

	// typing maps a stream ID to the cancel func of its typing keep-alive.
	typing sync.Map
}

// NewAdapter creates a WhatsApp adapter.
func NewAdapter(accessToken, phoneNumberID, appSecret, verifyToken string) *Adapter {
	return &Adapter{
		accessToken:   strings.TrimSpace(accessToken),
		phoneNumberID: strings.TrimSpace(phoneNumberID),
		appSecret:     strings.TrimSpace(appSecret),
		verifyToken:   strings.TrimSpace(verifyToken),
		apiBaseURL:    graphAPIBaseURL,
		httpClient: secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
			Timeout:      15 * time.Second,
			MaxRedirects: 5,
		}),
	}
}

// Platform returns the WhatsApp platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformWhatsApp
}

// HandleURLVerification answers Meta's webhook handshake:
// GET ?hub.mode=subscribe&hub.verify_token=<token>&hub.challenge=<nonce>.
// Every GET is treated as a handshake attempt, since events are always POSTed.
func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet {
		return false
	}

	token := c.Query("hub.verify_token")
	if a.verifyToken == "" || c.Query("hub.mode") != "subscribe" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(a.verifyToken)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "verification failed"})
		return true
	}

	c.String(http.StatusOK, c.Query("hub.challenge"))
	return true
}

// VerifyCallback checks the X-Hub-Signature-256 header, an HMAC-SHA256 of the
// raw request body keyed with the Meta app secret.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.appSecret == "" {
		return fmt.Errorf("app secret is required")
	}

	header := c.GetHeader(signatureHeader)
	if !strings.HasPrefix(header, signaturePrefix) {
		return fmt.Errorf("missing %s header", signatureHeader)
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, signaturePrefix))
	if err != nil {
		return fmt.Errorf("malformed signature")
	}

	body, err := readBody(c)
	if err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(a.appSecret))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

// readBody reads the capped callback body and restores it for the next reader.
func readBody(c *gin.Context) ([]byte, error) {
	im.LimitWebhookBody(c)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// webhookPayload is the subset of the Meta webhook envelope the adapter reads.
type webhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string       `json:"field"`
			Value webhookValue `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type webhookValue struct {
	Metadata struct {
		PhoneNumberID string `json:"phone_number_id"`
	} `json:"metadata"`
	Contacts []struct {
		WaID    string `json:"wa_id"`
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
	} `json:"contacts"`
	Messages []webhookMessage `json:"messages"`
}

type webhookMessage struct {
	From string `json:"from"`
	ID   string `json:"id"`
	Type string `json:"type"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`
}

// ParseCallback extracts the incoming text message from a Meta webhook event.
// It returns a nil message for events that carry nothing to answer.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	body, err := readBody(c)
	if err != nil {
		return nil, err
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse webhook payload: %w", err)
	}

	msg, skipped := a.parsePayload(&payload)
	if skipped > 0 {
		logger.Infof(c.Request.Context(), "[WhatsApp] Skipped %d unsupported or extra message(s) in callback", skipped)
	}
	return msg, nil
}

// parsePayload returns the first text message addressed to this adapter's
// phone number, plus how many other messages were left unhandled. Delivery
// receipts ("statuses") and events for other numbers of the same Meta app
// yield no message. Meta delivers one message per callback in practice.
func (a *Adapter) parsePayload(payload *webhookPayload) (*im.IncomingMessage, int) {
	if payload.Object != "whatsapp_business_account" {
		return nil, 0
	}

	var first *im.IncomingMessage
	skipped := 0
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			value := change.Value
			if change.Field != "messages" || value.Metadata.PhoneNumberID != a.phoneNumberID {
				continue
			}
			for _, m := range value.Messages {
				content := strings.TrimSpace(m.Text.Body)
				if first != nil || m.Type != "text" || content == "" || m.From == "" {
					skipped++
					continue
				}

				userName := ""
				for _, contact := range value.Contacts {
					if contact.WaID == m.From {
						userName = contact.Profile.Name
						break
					}
				}

				first = &im.IncomingMessage{
					Platform:    im.PlatformWhatsApp,
					MessageType: im.MessageTypeText,
					UserID:      m.From,
					UserName:    userName,
					ChatType:    im.ChatTypeDirect,
					Content:     content,
					MessageID:   m.ID,
				}
			}
		}
	}
	return first, skipped
}

// SendReply sends the answer as one or more WhatsApp text messages.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	if incoming.UserID == "" {
		return fmt.Errorf("missing recipient")
	}

	text := toWhatsAppText(im.FormatIMDisplayContent(reply.Content, im.StreamDisplayFinal))
	for _, chunk := range splitText(text, maxTextLength) {
		if err := a.sendText(ctx, incoming.UserID, chunk); err != nil {
			return err
		}
	}
	return nil
}

// SupportsFullOutputProgress reports that StartStream gives immediate progress
// feedback (the typing indicator) that the final answer supersedes.
func (a *Adapter) SupportsFullOutputProgress() bool {
	return true
}

// StartStream marks the incoming message as read and shows the typing
// indicator. WhatsApp clears it when a message is sent, or after 25 seconds,
// so it is re-sent periodically until the stream is finalized.
func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	streamID := incoming.MessageID
	if streamID == "" {
		return "", fmt.Errorf("missing message ID")
	}
	if err := a.sendTyping(ctx, streamID); err != nil {
		return "", err
	}

	keepCtx, cancel := context.WithTimeout(ctx, typingMaxDuration)
	a.typing.Store(streamID, cancel)
	go a.keepTyping(keepCtx, streamID, typingRefreshInterval)
	return streamID, nil
}

func (a *Adapter) sendTyping(ctx context.Context, messageID string) error {
	return a.postMessage(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageID,
		"typing_indicator":  map[string]any{"type": "text"},
	})
}

// keepTyping refreshes the typing indicator every interval until ctx is done.
func (a *Adapter) keepTyping(ctx context.Context, messageID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendTyping(ctx, messageID); err != nil {
				if ctx.Err() == nil {
					logger.Warnf(ctx, "[WhatsApp] Typing indicator refresh failed: %v", err)
				}
				return
			}
		}
	}
}

func (a *Adapter) stopTyping(streamID string) {
	if cancel, ok := a.typing.LoadAndDelete(streamID); ok {
		cancel.(context.CancelFunc)()
	}
}

// UpdateStreamContent is a no-op: a sent WhatsApp message cannot be edited, so
// intermediate content is never shown.
func (a *Adapter) UpdateStreamContent(context.Context, *im.IncomingMessage, string, string) error {
	return nil
}

// FinalizeStream sends the completed answer, which also clears the typing indicator.
func (a *Adapter) FinalizeStream(
	ctx context.Context, incoming *im.IncomingMessage, streamID string, finalContent string,
) error {
	a.stopTyping(streamID)
	return a.SendReply(ctx, incoming, &im.ReplyMessage{Content: finalContent, IsFinal: true})
}

// EndStream stops the typing keep-alive; there is no platform-side stream to close.
func (a *Adapter) EndStream(_ context.Context, _ *im.IncomingMessage, streamID string) error {
	a.stopTyping(streamID)
	return nil
}

func (a *Adapter) sendText(ctx context.Context, to, text string) error {
	return a.postMessage(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text":              map[string]any{"body": text},
	})
}

// postMessage POSTs body to the phone number's Graph API messages endpoint.
func (a *Adapter) postMessage(ctx context.Context, body map[string]any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	endpoint := fmt.Sprintf("%s/%s/messages", a.apiBaseURL, url.PathEscape(a.phoneNumberID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var apiErr struct {
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := im.DecodeIMResponse(resp.Body, &apiErr); err != nil || apiErr.Error.Message == "" {
		return fmt.Errorf("whatsapp API failed with status %d", resp.StatusCode)
	}
	return fmt.Errorf("whatsapp API failed with status %d: %s (code %d)",
		resp.StatusCode, apiErr.Error.Message, apiErr.Error.Code)
}
