// Package feishu implements the Feishu (飞书/Lark) IM adapter for WeKnora.
//
// Feishu bot flow:
// 1. User sends a message to the bot (direct or @mention in group)
// 2. Feishu calls our event subscription URL with the message event
// 3. We parse the event, run QA, then call Feishu API to send reply
// 4. For streaming: create a card, then use CardKit streaming update API
//
// Reference: https://open.feishu.cn/document/server-docs/im-v1/message/create
package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Adapter implements im.Adapter for Feishu/Lark.
type Adapter struct {
	appID             string
	appSecret         string
	verificationToken string
	encryptKey        string

	// Token cache
	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

// NewAdapter creates a new Feishu adapter.
func NewAdapter(appID, appSecret, verificationToken, encryptKey string) *Adapter {
	return &Adapter{
		appID:             appID,
		appSecret:         appSecret,
		verificationToken: verificationToken,
		encryptKey:        encryptKey,
	}
}

// Platform returns the platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformFeishu
}

// VerifyCallback verifies the Feishu event callback by checking the verification token.
// If no verification token is configured (e.g., WebSocket mode), skip verification.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.verificationToken == "" {
		return nil
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	// Always restore body for subsequent reads (ParseCallback)
	defer func() { c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes)) }()

	var raw []byte

	// Handle encrypted events
	var encryptedBody struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(bodyBytes, &encryptedBody); err == nil && encryptedBody.Encrypt != "" {
		decrypted, err := a.decrypt(encryptedBody.Encrypt)
		if err != nil {
			return fmt.Errorf("decrypt event for verification: %w", err)
		}
		raw = decrypted
	} else {
		raw = bodyBytes
	}

	var eventBody struct {
		Header *feishuEventHeader `json:"header"`
	}
	if err := json.Unmarshal(raw, &eventBody); err != nil {
		return fmt.Errorf("unmarshal event header: %w", err)
	}

	if eventBody.Header == nil || eventBody.Header.Token != a.verificationToken {
		return fmt.Errorf("invalid verification token")
	}

	return nil
}

// HandleURLVerification handles the Feishu URL verification challenge.
func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Try to parse as a challenge request
	var body map[string]interface{}

	// If encrypted, try to decrypt first
	var encryptedBody struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(bodyBytes, &encryptedBody); err == nil && encryptedBody.Encrypt != "" {
		decrypted, err := a.decrypt(encryptedBody.Encrypt)
		if err != nil {
			logger.Errorf(c.Request.Context(), "[Feishu] Failed to decrypt: %v", err)
			return false
		}
		if err := json.Unmarshal(decrypted, &body); err != nil {
			return false
		}
	} else {
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			return false
		}
	}

	// Check if this is a URL verification challenge
	if challenge, ok := body["challenge"].(string); ok {
		c.JSON(http.StatusOK, gin.H{"challenge": challenge})
		return true
	}

	// Reset body for subsequent reads
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return false
}

// feishuEventBody is the typed structure of a Feishu event callback.
type feishuEventBody struct {
	Header *feishuEventHeader `json:"header"`
	Event  *feishuEvent       `json:"event"`
}

type feishuEventHeader struct {
	EventType string `json:"event_type"`
	Token     string `json:"token"`
}

type feishuEvent struct {
	Message *feishuMessage `json:"message"`
	Sender  *feishuSender  `json:"sender"`
}

type feishuMessage struct {
	MessageID   string `json:"message_id"`
	MessageType string `json:"message_type"`
	ChatType    string `json:"chat_type"`
	ChatID      string `json:"chat_id"`
	Content     string `json:"content"`
}

type feishuSender struct {
	SenderID *feishuSenderID `json:"sender_id"`
}

type feishuSenderID struct {
	OpenID string `json:"open_id"`
}

// ParseCallback parses a Feishu event callback into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var raw []byte

	// Handle encrypted events
	var encryptedBody struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(bodyBytes, &encryptedBody); err == nil && encryptedBody.Encrypt != "" {
		decrypted, err := a.decrypt(encryptedBody.Encrypt)
		if err != nil {
			return nil, fmt.Errorf("decrypt event: %w", err)
		}
		raw = decrypted
	} else {
		raw = bodyBytes
	}

	var eventBody feishuEventBody
	if err := json.Unmarshal(raw, &eventBody); err != nil {
		return nil, fmt.Errorf("unmarshal event: %w", err)
	}

	// Token verification is handled by VerifyCallback; no need to re-check here.

	// Check event type
	if eventBody.Header == nil || eventBody.Header.EventType != "im.message.receive_v1" {
		if eventBody.Header != nil {
			logger.Infof(c.Request.Context(), "[Feishu] Ignoring event type: %s", eventBody.Header.EventType)
		}
		return nil, nil
	}

	// Extract message info
	if eventBody.Event == nil || eventBody.Event.Message == nil {
		return nil, nil
	}
	msg := eventBody.Event.Message

	if msg.MessageType != "text" {
		logger.Infof(c.Request.Context(), "[Feishu] Ignoring non-text message type: %s", msg.MessageType)
		return nil, nil
	}

	// Parse text content
	var textContent struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(msg.Content), &textContent); err != nil {
		return nil, fmt.Errorf("unmarshal text content: %w", err)
	}

	// Determine chat type
	chatType := im.ChatTypeDirect
	chatID := ""
	if msg.ChatType == "group" {
		chatType = im.ChatTypeGroup
		chatID = msg.ChatID
	}

	// Get sender info
	openID := ""
	if eventBody.Event.Sender != nil && eventBody.Event.Sender.SenderID != nil {
		openID = eventBody.Event.Sender.SenderID.OpenID
	}

	// Strip @bot mention from group messages
	content := textContent.Text
	if chatType == im.ChatTypeGroup {
		// Feishu @mentions are in the format @_user_xxx
		for strings.HasPrefix(content, "@_user_") {
			idx := strings.Index(content, " ")
			if idx >= 0 {
				content = content[idx+1:]
			} else {
				break
			}
		}
	}

	return &im.IncomingMessage{
		Platform:  im.PlatformFeishu,
		UserID:    openID,
		ChatID:    chatID,
		ChatType:  chatType,
		Content:   strings.TrimSpace(content),
		MessageID: msg.MessageID,
	}, nil
}

// SendReply sends a reply message via Feishu API.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	accessToken, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// Determine receive_id_type and receive_id
	receiveIDType := "open_id"
	receiveID := incoming.UserID
	if incoming.ChatType == im.ChatTypeGroup && incoming.ChatID != "" {
		receiveIDType = "chat_id"
		receiveID = incoming.ChatID
	}

	// Build text message
	content, _ := json.Marshal(map[string]string{"text": reply.Content})
	payload := map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	}

	payloadBytes, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("feishu api error: code=%d msg=%s", result.Code, result.Msg)
	}

	return nil
}

// getTenantAccessToken retrieves the Feishu tenant access token with caching.
// Feishu tokens expire in 2 hours; we cache with a safety margin.
func (a *Adapter) getTenantAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	if a.tokenCache != "" && time.Now().Before(a.tokenExpAt) {
		return a.tokenCache, nil
	}

	payload, _ := json.Marshal(map[string]string{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // seconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("get token error: code=%d msg=%s", result.Code, result.Msg)
	}

	a.tokenCache = result.TenantAccessToken
	// Cache with 5-minute safety margin
	ttl := time.Duration(result.Expire) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	a.tokenExpAt = time.Now().Add(ttl)

	return a.tokenCache, nil
}

// decrypt decrypts a Feishu encrypted event body.
// Feishu uses AES-256-CBC with SHA-256 of the encrypt key as the AES key.
func (a *Adapter) decrypt(encrypted string) ([]byte, error) {
	if a.encryptKey == "" {
		return nil, fmt.Errorf("encrypt_key not configured")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	// SHA-256 of encrypt key as AES key
	keyHash := sha256.Sum256([]byte(a.encryptKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)

	// Remove and verify PKCS#7 padding
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("empty plaintext")
	}
	padLen := int(ciphertext[len(ciphertext)-1])
	if padLen > aes.BlockSize || padLen == 0 || padLen > len(ciphertext) {
		return nil, fmt.Errorf("invalid padding")
	}
	for i := 0; i < padLen; i++ {
		if ciphertext[len(ciphertext)-1-i] != byte(padLen) {
			return nil, fmt.Errorf("invalid padding")
		}
	}

	return ciphertext[:len(ciphertext)-padLen], nil
}
