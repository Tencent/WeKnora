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
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// WebhookAdapter implements im.Adapter for WeCom in webhook (self-built app callback) mode.
// Messages arrive via HTTP callback; replies are sent via the WeCom REST API.
type WebhookAdapter struct {
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

// Compile-time check that WebhookAdapter implements im.FileDownloader.
var _ im.FileDownloader = (*WebhookAdapter)(nil)

// NewWebhookAdapter creates a new WeCom webhook adapter.
func NewWebhookAdapter(corpID, agentSecret, token, encodingAESKey string, corpAgentID int) (*WebhookAdapter, error) {
	// Decode the AES key from base64
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("decode encoding_aes_key: %w", err)
	}

	return &WebhookAdapter{
		corpID:         corpID,
		token:          token,
		encodingAESKey: encodingAESKey,
		aesKey:         aesKey,
		agentSecret:    agentSecret,
		corpAgentID:    corpAgentID,
	}, nil
}

// Platform returns the platform identifier.
func (a *WebhookAdapter) Platform() im.Platform {
	return im.PlatformWeCom
}

// VerifyCallback verifies the WeCom callback signature.
func (a *WebhookAdapter) VerifyCallback(c *gin.Context) error {
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
func (a *WebhookAdapter) HandleURLVerification(c *gin.Context) bool {
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
func (a *WebhookAdapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
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

	// Log raw decrypted message for debugging
	logger.Debugf(c.Request.Context(), "[WeCom] Raw decrypted callback: %s", string(decrypted))

	var msg wecomMessage
	if err := xml.Unmarshal(decrypted, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal decrypted message: %w", err)
	}

	logger.Debugf(c.Request.Context(), "[WeCom] Parsed webhook message: msgid=%s msgtype=%s from=%s content=%q picurl=%q mediaid=%q",
		msg.MsgID, msg.MsgType, msg.FromUserName, msg.Content, msg.PicUrl, msg.MediaId)

	// Determine chat type
	chatType := im.ChatTypeDirect
	chatID := ""
	isGroup := msg.ChatID != ""
	if isGroup {
		chatType = im.ChatTypeGroup
		chatID = msg.ChatID
	}

	switch msg.MsgType {
	case "text":
		// Strip @mention in group chat (same issue as long connection mode).
		// Webhook adapter has no persistent state, so use the standalone helper.
		textContent := msg.Content
		if isGroup {
			textContent = stripAtMentionBasic(textContent)
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeText,
			UserID:      msg.FromUserName,
			UserName:    msg.FromUserName,
			ChatID:      chatID,
			ChatType:    chatType,
			Content:     strings.TrimSpace(textContent),
			MessageID:   msg.MsgID,
		}, nil

	case "image":
		// Image via webhook: has PicUrl (direct download) and MediaId
		if msg.PicUrl == "" && msg.MediaId == "" {
			return nil, nil
		}
		fileKey := msg.PicUrl
		if fileKey == "" {
			fileKey = msg.MediaId
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformWeCom,
			MessageType: im.MessageTypeImage,
			UserID:      msg.FromUserName,
			UserName:    msg.FromUserName,
			ChatID:      chatID,
			ChatType:    chatType,
			MessageID:   msg.MsgID,
			FileKey:     fileKey,
			FileName:    msg.MsgID + ".png",
		}, nil

	default:
		logger.Infof(c.Request.Context(), "[WeCom] Ignoring unsupported message type: %s", msg.MsgType)
		return nil, nil
	}
}

// SendReply sends a reply message via WeCom API.
// For group chats, it tries the appchat API first to reply in the group,
// then falls back to sending a direct message to the user.
func (a *WebhookAdapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// For group chats, try sending to the group via appchat API first.
	// This works for groups created via /cgi-bin/appchat/create.
	if incoming.ChatType == im.ChatTypeGroup && incoming.ChatID != "" {
		if err := a.sendToAppChat(ctx, accessToken, incoming.ChatID, reply); err == nil {
			return nil
		}
		logger.Debugf(ctx, "[WeCom] appchat/send failed for chat=%s, falling back to touser: %v", incoming.ChatID, err)
	}

	// Fallback (or direct message): send to the user directly.
	return a.sendToUser(ctx, accessToken, incoming.UserID, reply)
}

// sendToAppChat sends a message to a WeCom group chat via the appchat API.
// Reference: https://developer.work.weixin.qq.com/document/path/90248
func (a *WebhookAdapter) sendToAppChat(ctx context.Context, accessToken, chatID string, reply *im.ReplyMessage) error {
	payload := map[string]interface{}{
		"chatid":  chatID,
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": reply.Content,
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	sendURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/appchat/send?access_token=%s", accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send appchat message: %w", err)
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
		return fmt.Errorf("appchat api error: code=%d msg=%s", result.ErrCode, result.ErrMsg)
	}

	return nil
}

// sendToUser sends a message directly to a user via the application message API.
// Reference: https://developer.work.weixin.qq.com/document/path/90236
func (a *WebhookAdapter) sendToUser(ctx context.Context, accessToken, userID string, reply *im.ReplyMessage) error {
	payload := map[string]interface{}{
		"touser":  userID,
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
func (a *WebhookAdapter) getAccessToken(ctx context.Context) (string, error) {
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
func (a *WebhookAdapter) verifySignature(signature, timestamp, nonce, encrypt string) bool {
	parts := []string{a.token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	combined := strings.Join(parts, "")

	hash := sha1.New()
	hash.Write([]byte(combined))
	computed := fmt.Sprintf("%x", hash.Sum(nil))

	return hmac.Equal([]byte(computed), []byte(signature))
}

// decrypt decrypts a WeCom AES-encrypted message.
func (a *WebhookAdapter) decrypt(encrypted string) ([]byte, error) {
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
// Supports text, image, voice, video, location, and link message types.
// Reference: https://developer.work.weixin.qq.com/document/path/90375
type wecomMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`      // text
	PicUrl       string   `xml:"PicUrl"`        // image: download URL
	MediaId      string   `xml:"MediaId"`       // image/voice/video: media ID for download
	Format       string   `xml:"Format"`        // voice: audio format (amr/speex)
	ThumbMediaId string   `xml:"ThumbMediaId"`  // video: thumbnail media ID
	MsgID        string   `xml:"MsgId"`
	AgentID      string   `xml:"AgentID"`
	ChatID       string   `xml:"ChatId"`
}

// ──────────────────────────────────────────────────────────────────────
// File download support for WeCom webhook mode
// ──────────────────────────────────────────────────────────────────────

// DownloadFile downloads a file/image from WeCom.
// For webhook mode, images come with MediaId (temporary media) which can be
// downloaded via the GetMedia API, or PicUrl for direct download.
func (a *WebhookAdapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("no file key (URL or media_id) in message")
	}

	fileName := msg.FileName
	if fileName == "" {
		fileName = msg.FileKey
	}

	// If FileKey looks like a URL, download directly
	if strings.HasPrefix(msg.FileKey, "http://") || strings.HasPrefix(msg.FileKey, "https://") {
		return downloadFromURL(ctx, msg.FileKey, fileName)
	}

	// Otherwise treat as media_id, download via temporary media API
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get access token: %w", err)
	}

	apiURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/media/get?access_token=%s&media_id=%s",
		accessToken, msg.FileKey)
	return downloadFromURL(ctx, apiURL, fileName)
}

// downloadFromURL performs a GET request and returns the response body.
// It tries to resolve the real filename from HTTP response headers:
//  1. Content-Disposition: attachment; filename="xxx.pdf"
//  2. Content-Type → extension mapping (fallback for platforms like WeCom that
//     don't provide the original filename in the callback JSON)
func downloadFromURL(ctx context.Context, rawURL, fileName string) (io.ReadCloser, string, error) {
	// SSRF protection: reject internal/private URLs unless on the WeCom API allowlist.
	if !isAllowedIMAPIHost(rawURL) {
		if err := secutils.ValidateURLForSSRF(rawURL); err != nil {
			return nil, "", fmt.Errorf("URL rejected for security reasons: %v", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("download failed: status=%d", resp.StatusCode)
	}

	logger.Debugf(ctx, "[WeCom] Download response: status=%d content-type=%s content-disposition=%s",
		resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))

	// Try to extract filename from Content-Disposition header.
	// Supports both standard filename and RFC 5987 filename* parameters.
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			// Prefer filename* (RFC 5987, already decoded by mime.ParseMediaType)
			if fn := params["filename"]; fn != "" {
				fileName = fn
			}
		} else {
			// Fallback: manual extraction for malformed headers
			if idx := strings.Index(cd, "filename="); idx >= 0 {
				extracted := strings.Trim(cd[idx+len("filename="):], "\" ")
				if extracted != "" {
					fileName = extracted
				}
			}
		}
	}

	// URL-decode the filename if it contains percent-encoded characters.
	// Some servers (e.g. WeCom COS) return URL-encoded Chinese filenames.
	if strings.Contains(fileName, "%") {
		if decoded, err := url.QueryUnescape(fileName); err == nil && decoded != "" {
			fileName = decoded
		}
	}

	// Also try to extract a meaningful filename from the URL path itself,
	// in case Content-Disposition is missing but the URL contains the real name.
	if !strings.Contains(fileName, ".") {
		if u, err := url.Parse(rawURL); err == nil {
			base := path.Base(u.Path)
			if base != "" && base != "." && base != "/" && strings.Contains(base, ".") {
				// URL-decode the path component as well
				if decoded, err := url.QueryUnescape(base); err == nil {
					fileName = decoded
				} else {
					fileName = base
				}
			}
		}
	}

	// If filename still has no extension, try to infer from Content-Type.
	// This handles platforms (e.g. WeCom aibot) where the callback only provides
	// a hash ID as the filename without any extension.
	if !strings.Contains(fileName, ".") {
		if ext := contentTypeToExt(resp.Header.Get("Content-Type")); ext != "" {
			fileName = fileName + "." + ext
		}
	}

	return resp.Body, fileName, nil
}

// allowedIMAPIHosts lists IM platform API hosts that are trusted for file downloads.
// URLs pointing to these hosts bypass isSSRFSafeURL checks because the WeCom API
// itself returns these URLs in callback payloads (e.g. temporary media links).
var allowedIMAPIHosts = []string{
	"qyapi.weixin.qq.com",
	"api.weixin.qq.com",
	"open.work.weixin.qq.com",
}

// isAllowedIMAPIHost returns true if rawURL points to a known IM platform API host.
func isAllowedIMAPIHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	hostname := strings.ToLower(u.Hostname())
	for _, allowed := range allowedIMAPIHosts {
		if hostname == allowed {
			return true
		}
	}
	return false
}

// contentTypeToExt maps common Content-Type values to file extensions.
func contentTypeToExt(ct string) string {
	// Normalize: take only the media type, ignore parameters like charset
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	ct = strings.ToLower(ct)

	mapping := map[string]string{
		"application/pdf":                                                 "pdf",
		"application/msword":                                              "doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
		"application/vnd.ms-excel":                                        "xls",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
		"application/vnd.ms-powerpoint":                                   "ppt",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
		"text/plain":       "txt",
		"text/markdown":    "md",
		"text/csv":         "csv",
		"image/png":        "png",
		"image/jpeg":       "jpg",
		"image/gif":        "gif",
		"image/webp":       "webp",
	}

	return mapping[ct]
}
