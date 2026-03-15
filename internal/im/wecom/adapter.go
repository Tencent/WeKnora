// Package wecom implements the WeCom (企业微信) IM adapter for WeKnora.
//
// WeCom Smart Bot flow:
// 1. User sends a message to the bot (direct or @mention in group)
// 2. WeCom calls our callback URL with the encrypted message
// 3. We decrypt, parse, and return an immediate response (or stream response)
// 4. For streaming: respond with msgtype="stream", WeCom pulls subsequent chunks via refresh callbacks
//
// Reference: https://developer.work.weixin.qq.com/document/path/101031
package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Adapter implements im.Adapter for WeCom.
type Adapter struct {
	corpID         string
	token          string
	encodingAESKey string
	aesKey         []byte
	agentSecret    string
	corpAgentID    int

	// Token cache
	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

// NewAdapter creates a new WeCom adapter.
func NewAdapter(corpID, agentSecret, token, encodingAESKey string, corpAgentID int) (*Adapter, error) {
	// Decode the AES key from base64
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("decode encoding_aes_key: %w", err)
	}

	return &Adapter{
		corpID:         corpID,
		token:          token,
		encodingAESKey: encodingAESKey,
		aesKey:         aesKey,
		agentSecret:    agentSecret,
		corpAgentID:    corpAgentID,
	}, nil
}

// Platform returns the platform identifier.
func (a *Adapter) Platform() im.Platform {
	return im.PlatformWeCom
}

// VerifyCallback verifies the WeCom callback signature.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")
	msgSignature := c.Query("msg_signature")

	// For GET requests (URL verification), use echostr
	// For POST requests (message callback), use request body's Encrypt field
	var encrypt string
	if c.Request.Method == http.MethodGet {
		encrypt = c.Query("echostr")
	} else {
		var body callbackRequestBody
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return fmt.Errorf("read request body: %w", err)
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		if err := xml.Unmarshal(bodyBytes, &body); err != nil {
			return fmt.Errorf("unmarshal xml body: %w", err)
		}
		encrypt = body.Encrypt
	}

	if !a.verifySignature(msgSignature, timestamp, nonce, encrypt) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// HandleURLVerification handles the WeCom URL verification (GET request).
func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet {
		return false
	}

	echoStr := c.Query("echostr")
	if echoStr == "" {
		return false
	}

	// Decrypt the echostr and return it
	decrypted, err := a.decrypt(echoStr)
	if err != nil {
		logger.Errorf(c.Request.Context(), "[WeCom] Failed to decrypt echostr: %v", err)
		c.String(http.StatusBadRequest, "decrypt failed")
		return true
	}

	c.String(http.StatusOK, string(decrypted))
	return true
}

// ParseCallback parses a WeCom callback into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var body callbackRequestBody
	if err := xml.Unmarshal(bodyBytes, &body); err != nil {
		return nil, fmt.Errorf("unmarshal xml: %w", err)
	}

	// Decrypt the message
	decrypted, err := a.decrypt(body.Encrypt)
	if err != nil {
		return nil, fmt.Errorf("decrypt message: %w", err)
	}

	var msg wecomMessage
	if err := xml.Unmarshal(decrypted, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal decrypted message: %w", err)
	}

	// Only handle text messages
	if msg.MsgType != "text" {
		logger.Infof(c.Request.Context(), "[WeCom] Ignoring non-text message type: %s", msg.MsgType)
		return nil, nil
	}

	chatType := im.ChatTypeDirect
	chatID := ""
	content := msg.Content

	// Check if this is a group message (has ChatId field in WeCom smart bot callback)
	// In group chats, the content may contain @bot mention prefix that should be stripped
	if msg.ChatID != "" {
		chatType = im.ChatTypeGroup
		chatID = msg.ChatID
	}

	return &im.IncomingMessage{
		Platform:  im.PlatformWeCom,
		UserID:    msg.FromUserName,
		UserName:  msg.FromUserName,
		ChatID:    chatID,
		ChatType:  chatType,
		Content:   strings.TrimSpace(content),
		MessageID: msg.MsgID,
	}, nil
}

// SendReply sends a reply message via WeCom API.
// For simplicity, this uses the "应用消息" API to proactively send messages.
// A production implementation would integrate with the streaming callback model.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	// Get access token
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// Build message payload.
	// Note: /cgi-bin/message/send only supports touser/toparty/totag — it does NOT
	// support regular group chat IDs (chatid is only for appchat-created groups).
	// So for both direct and group messages we reply to the user directly.
	payload := map[string]interface{}{
		"touser":  incoming.UserID,
		"msgtype": "markdown",
		"agentid": a.corpAgentID,
		"markdown": map[string]string{
			"content": reply.Content,
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	sendURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=%s", accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wecom api error: code=%d msg=%s", result.ErrCode, result.ErrMsg)
	}

	return nil
}

// getAccessToken retrieves the WeCom access token with caching.
// WeCom tokens expire in 7200 seconds (2 hours); we cache with a safety margin.
func (a *Adapter) getAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	if a.tokenCache != "" && time.Now().Before(a.tokenExpAt) {
		return a.tokenCache, nil
	}

	tokenURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		a.corpID, a.agentSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request access token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("get token error: code=%d msg=%s", result.ErrCode, result.ErrMsg)
	}

	a.tokenCache = result.AccessToken
	// Cache with 5-minute safety margin
	ttl := time.Duration(result.ExpiresIn) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	a.tokenExpAt = time.Now().Add(ttl)

	return a.tokenCache, nil
}

// verifySignature verifies the WeCom callback signature using constant-time comparison.
func (a *Adapter) verifySignature(signature, timestamp, nonce, encrypt string) bool {
	parts := []string{a.token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	combined := strings.Join(parts, "")

	hash := sha1.New()
	hash.Write([]byte(combined))
	computed := fmt.Sprintf("%x", hash.Sum(nil))

	return hmac.Equal([]byte(computed), []byte(signature))
}

// decrypt decrypts a WeCom AES-encrypted message.
func (a *Adapter) decrypt(encrypted string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	block, err := aes.NewCipher(a.aesKey)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	iv := a.aesKey[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)

	// Remove and verify PKCS#7 padding
	padLen := int(ciphertext[len(ciphertext)-1])
	if padLen > aes.BlockSize || padLen == 0 || padLen > len(ciphertext) {
		return nil, fmt.Errorf("invalid padding")
	}
	for i := 0; i < padLen; i++ {
		if ciphertext[len(ciphertext)-1-i] != byte(padLen) {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	plaintext := ciphertext[:len(ciphertext)-padLen]

	// WeCom format: random(16) + msg_len(4) + msg + corp_id
	if len(plaintext) < 20 {
		return nil, fmt.Errorf("plaintext too short")
	}

	msgLen := binary.BigEndian.Uint32(plaintext[16:20])
	if uint32(len(plaintext)) < 20+msgLen {
		return nil, fmt.Errorf("message length mismatch")
	}

	msgBytes := plaintext[20 : 20+msgLen]

	// Verify corp_id from plaintext tail
	corpIDBytes := plaintext[20+msgLen:]
	if string(corpIDBytes) != a.corpID {
		return nil, fmt.Errorf("corp_id mismatch: expected %s, got %s", a.corpID, string(corpIDBytes))
	}

	return msgBytes, nil
}

// callbackRequestBody is the XML structure of a WeCom callback request body.
type callbackRequestBody struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	Encrypt    string   `xml:"Encrypt"`
	AgentID    string   `xml:"AgentID"`
}

// wecomMessage is the decrypted WeCom message structure.
type wecomMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
	AgentID      string   `xml:"AgentID"`
	ChatID       string   `xml:"ChatId"`
}
