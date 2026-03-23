// Long-polling client for the WeChat iLink Bot API.
//
// Flow:
//  1. POST /ilink/bot/getupdates with get_updates_buf + base_info
//  2. Parse response msgs[] into IncomingMessage
//  3. Call msgHandler for each message
//  4. Update cursor (get_updates_buf) for next poll
//  5. On error, exponential backoff retry
//
// Token expiry: errcode -14 signals the token is no longer valid.
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	longPollTimeout     = 35 * time.Second
	longPollHTTPTimeout = 40 * time.Second // slightly longer than poll timeout
	reconnectBaseDelay  = 1 * time.Second
	reconnectMaxDelay   = 30 * time.Second
	maxReconnectAttempts = -1 // infinite
)

// ErrTokenExpired indicates the bot token has expired and a re-login is required.
var ErrTokenExpired = fmt.Errorf("wechat bot token expired")

// LongPollClient receives messages from WeChat via HTTP long-polling.
type LongPollClient struct {
	botToken   string
	ilinkBotID string
	handler    func(ctx context.Context, msg *im.IncomingMessage) error
	httpClient *http.Client
	cursor     string // get_updates_buf: opaque cursor for pagination
}

// NewLongPollClient creates a new WeChat long-polling client.
func NewLongPollClient(botToken, ilinkBotID string, handler func(ctx context.Context, msg *im.IncomingMessage) error) *LongPollClient {
	return &LongPollClient{
		botToken:   botToken,
		ilinkBotID: ilinkBotID,
		handler:    handler,
		httpClient: &http.Client{Timeout: longPollHTTPTimeout},
	}
}

// Start begins the long-polling loop. It reconnects automatically on transient errors.
// Returns ErrTokenExpired when the bot token expires (errcode -14).
func (c *LongPollClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] WeChat long-poll starting (bot_id=%s)...", c.ilinkBotID)

	attempts := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		pollStart := time.Now()
		err := c.poll(ctx)
		if err == nil {
			// Successful poll — reset attempts
			attempts = 0
			continue
		}

		if err == ErrTokenExpired {
			logger.Warnf(ctx, "[WeChat] Bot token expired, stopping long-poll")
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// If we ran for a while before failing, reset backoff
		if time.Since(pollStart) > reconnectMaxDelay {
			attempts = 0
		}

		attempts++
		if maxReconnectAttempts >= 0 && attempts >= maxReconnectAttempts {
			return fmt.Errorf("max reconnect attempts reached: %w", err)
		}

		delay := pollReconnectDelay(attempts)
		logger.Warnf(ctx, "[WeChat] Poll error (%v), retrying in %v (attempt %d)...", err, delay, attempts)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// poll performs a single long-poll request to /ilink/bot/getupdates.
func (c *LongPollClient) poll(ctx context.Context) error {
	payload := map[string]interface{}{
		"get_updates_buf": c.cursor,
		"base_info":       newBaseInfo(),
	}

	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ilinkBaseURL+"/ilink/bot/getupdates", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("X-WECHAT-UIN", generateWeChatUIN())
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("poll request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("getupdates returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result getUpdatesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Token expired
	if result.ErrCode == -14 {
		return ErrTokenExpired
	}

	if result.Ret != 0 && result.ErrCode != 0 {
		return fmt.Errorf("getupdates error: ret=%d errcode=%d msg=%s", result.Ret, result.ErrCode, result.ErrMsg)
	}

	// Update cursor for next poll
	if result.GetUpdatesBuf != "" {
		c.cursor = result.GetUpdatesBuf
	}

	// Process messages
	for i := range result.Msgs {
		msg := &result.Msgs[i]
		incoming := c.parseMessage(msg)
		if incoming == nil {
			continue
		}

		// Handle in a detached goroutine so we don't block polling
		go func(m *im.IncomingMessage) {
			if err := c.handler(ctx, m); err != nil {
				logger.Errorf(ctx, "[WeChat] Handle message error: %v", err)
			}
		}(incoming)
	}

	return nil
}

// parseMessage converts a WeixinMessage from getupdates to a unified IncomingMessage.
func (c *LongPollClient) parseMessage(msg *weixinMessage) *im.IncomingMessage {
	contextToken := msg.ContextToken

	// Only process user messages (message_type=1), skip bot messages (message_type=2)
	if msg.MessageType == 2 {
		return nil
	}

	if len(msg.ItemList) == 0 {
		return nil
	}

	// Process the first item
	item := msg.ItemList[0]

	switch item.Type {
	case 1: // TEXT
		content := ""
		if item.TextItem != nil {
			content = strings.TrimSpace(item.TextItem.Text)
		}
		if content == "" {
			return nil
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformWeChat,
			MessageType: im.MessageTypeText,
			UserID:      msg.FromUserID,
			ChatType:    im.ChatTypeDirect,
			Content:     content,
			MessageID:   fmt.Sprintf("%d", msg.MessageID),
			Extra:       map[string]string{"context_token": contextToken},
		}

	case 2: // IMAGE
		if item.ImageItem == nil || item.ImageItem.Media == nil {
			return nil
		}
		encryptParam := item.ImageItem.Media.EncryptQueryParam
		if encryptParam == "" {
			return nil
		}
		// Build full CDN download URL from encrypt_query_param
		downloadURL := BuildCDNDownloadURL(encryptParam)
		// For images, prefer aeskey (hex format) from image_item, else media.aes_key (base64)
		aesKey := ""
		if item.ImageItem.AESKey != "" {
			// hex → base64 for uniform handling
			aesKey = item.ImageItem.AESKey
		} else if item.ImageItem.Media.AESKey != "" {
			aesKey = item.ImageItem.Media.AESKey
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformWeChat,
			MessageType: im.MessageTypeImage,
			UserID:      msg.FromUserID,
			ChatType:    im.ChatTypeDirect,
			MessageID:   fmt.Sprintf("%d", msg.MessageID),
			FileKey:     downloadURL,
			FileName:    fmt.Sprintf("%d.png", msg.MessageID),
			Extra: map[string]string{
				"context_token": contextToken,
				"aes_key":       aesKey,
			},
		}

	case 3: // VOICE (speech-to-text)
		if item.VoiceItem != nil && item.VoiceItem.Text != "" {
			return &im.IncomingMessage{
				Platform:    im.PlatformWeChat,
				MessageType: im.MessageTypeText,
				UserID:      msg.FromUserID,
				ChatType:    im.ChatTypeDirect,
				Content:     strings.TrimSpace(item.VoiceItem.Text),
				MessageID:   fmt.Sprintf("%d", msg.MessageID),
				Extra:       map[string]string{"context_token": contextToken},
			}
		}
		return nil

	case 4: // FILE
		if item.FileItem == nil || item.FileItem.Media == nil {
			return nil
		}
		encryptParam := item.FileItem.Media.EncryptQueryParam
		if encryptParam == "" {
			return nil
		}
		// Build full CDN download URL from encrypt_query_param
		downloadURL := BuildCDNDownloadURL(encryptParam)
		fileName := item.FileItem.FileName
		if fileName == "" {
			fileName = fmt.Sprintf("file_%d", msg.MessageID)
		}
		var fileSize int64
		if item.FileItem.Len != "" {
			fmt.Sscanf(item.FileItem.Len, "%d", &fileSize)
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformWeChat,
			MessageType: im.MessageTypeFile,
			UserID:      msg.FromUserID,
			ChatType:    im.ChatTypeDirect,
			MessageID:   fmt.Sprintf("%d", msg.MessageID),
			FileKey:     downloadURL,
			FileName:    fileName,
			FileSize:    fileSize,
			Extra: map[string]string{
				"context_token": contextToken,
				"aes_key":       item.FileItem.Media.AESKey,
			},
		}

	default:
		return nil
	}
}

func pollReconnectDelay(attempt int) time.Duration {
	delay := reconnectBaseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
	if delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}
	return delay
}

// ── iLink API response types (matches proto: GetUpdatesResp, WeixinMessage) ──

type getUpdatesResponse struct {
	Ret           int              `json:"ret"`
	ErrCode       int              `json:"errcode"`
	ErrMsg        string           `json:"errmsg"`
	Msgs          []weixinMessage  `json:"msgs"`
	GetUpdatesBuf string           `json:"get_updates_buf"`
}

type weixinMessage struct {
	Seq          int              `json:"seq"`
	MessageID    int64            `json:"message_id"`
	FromUserID   string           `json:"from_user_id"`
	ToUserID     string           `json:"to_user_id"`
	ClientID     string           `json:"client_id"`
	CreateTimeMs int64            `json:"create_time_ms"`
	SessionID    string           `json:"session_id"`
	MessageType  int              `json:"message_type"`  // 1=USER, 2=BOT
	MessageState int              `json:"message_state"` // 0=NEW, 1=GENERATING, 2=FINISH
	ItemList     []messageItem    `json:"item_list"`
	ContextToken string           `json:"context_token"`
}

type messageItem struct {
	Type      int        `json:"type"` // 1=TEXT, 2=IMAGE, 3=VOICE, 4=FILE, 5=VIDEO
	TextItem  *textItem  `json:"text_item,omitempty"`
	ImageItem *imageItem `json:"image_item,omitempty"`
	VoiceItem *voiceItem `json:"voice_item,omitempty"`
	FileItem  *fileItem  `json:"file_item,omitempty"`
}

type textItem struct {
	Text string `json:"text"`
}

type cdnMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
}

type imageItem struct {
	Media  *cdnMedia `json:"media,omitempty"`
	AESKey string    `json:"aeskey"` // hex string, preferred for inbound decryption
	URL    string    `json:"url,omitempty"`
}

type voiceItem struct {
	Media *cdnMedia `json:"media,omitempty"`
	Text  string    `json:"text"` // speech-to-text result
}

type fileItem struct {
	Media    *cdnMedia `json:"media,omitempty"`
	FileName string    `json:"file_name"`
	Len      string    `json:"len"` // plaintext bytes as string
}
