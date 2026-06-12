package telegram

import (
	"bytes"
	"context"
	"crypto/subtle"
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

// Compile-time checks.
var (
	_ im.Adapter        = (*Adapter)(nil)
	_ im.StreamSender   = (*Adapter)(nil)
	_ im.FileDownloader = (*Adapter)(nil)
)

// Adapter implements im.Adapter for Telegram Bot API.
type Adapter struct {
	botToken    string
	secretToken string // optional X-Telegram-Bot-Api-Secret-Token for webhook verification
	client      *LongConnClient
}

// NewWebhookAdapter creates a Telegram adapter for webhook mode.
func NewWebhookAdapter(botToken, secretToken string) *Adapter {
	startStreamReaper()
	return &Adapter{
		botToken:    botToken,
		secretToken: secretToken,
	}
}

// NewAdapter creates a Telegram adapter backed by a long-polling client.
func NewAdapter(client *LongConnClient, botToken string) *Adapter {
	startStreamReaper()
	return &Adapter{
		botToken: botToken,
		client:   client,
	}
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformTelegram
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	return false // Telegram does not require URL verification challenges.
}

func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.secretToken == "" {
		return nil
	}
	token := c.GetHeader("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.secretToken)) != 1 {
		return fmt.Errorf("invalid secret token")
	}
	return nil
}

// telegramUpdate represents an incoming Telegram update (subset of fields).
type telegramUpdate struct {
	UpdateID int          `json:"update_id"`
	Message  *telegramMsg `json:"message"`
}

type telegramMsg struct {
	MessageID       int             `json:"message_id"`
	MessageThreadID int             `json:"message_thread_id"`
	From            *telegramUser   `json:"from"`
	Chat            telegramChat    `json:"chat"`
	Text            string          `json:"text"`
	Document        *telegramDoc    `json:"document"`
	Photo           []telegramPhoto `json:"photo"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // "private", "group", "supergroup", "channel"
}

type telegramDoc struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
}

type telegramPhoto struct {
	FileID   string `json:"file_id"`
	FileSize int    `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var update telegramUpdate
	if err := json.Unmarshal(bodyBytes, &update); err != nil {
		return nil, fmt.Errorf("parse update: %w", err)
	}

	return parseUpdate(&update), nil
}

func parseUpdate(update *telegramUpdate) *im.IncomingMessage {
	if update.Message == nil {
		return nil
	}
	return parseTelegramMessage(update.Message)
}

func parseTelegramMessage(msg *telegramMsg) *im.IncomingMessage {
	if msg == nil {
		return nil
	}

	chatType := im.ChatTypeDirect
	chatID := ""
	if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
		chatType = im.ChatTypeGroup
		chatID = fmt.Sprintf("%d", msg.Chat.ID)
	}

	userID := ""
	userName := ""
	if msg.From != nil {
		userID = fmt.Sprintf("%d", msg.From.ID)
		userName = strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
		if userName == "" {
			userName = msg.From.Username
		}
	}

	threadID := ""
	if msg.MessageThreadID != 0 {
		threadID = fmt.Sprintf("%d", msg.MessageThreadID)
	}

	incoming := &im.IncomingMessage{
		Platform:    im.PlatformTelegram,
		UserID:      userID,
		UserName:    userName,
		ChatID:      chatID,
		ChatType:    chatType,
		MessageID:   fmt.Sprintf("%d", msg.MessageID),
		ThreadID:    threadID,
		MessageType: im.MessageTypeText,
		Content:     msg.Text,
	}

	// For group messages, strip bot mention prefix (e.g., "/command@botname text" -> "text")
	if chatType == im.ChatTypeGroup {
		content := strings.TrimSpace(msg.Text)
		// Remove @bot mentions
		if idx := strings.Index(content, " "); idx > 0 && strings.Contains(content[:idx], "@") {
			content = strings.TrimSpace(content[idx+1:])
		}
		incoming.Content = content
	}

	// Handle document
	if msg.Document != nil {
		incoming.MessageType = im.MessageTypeFile
		incoming.FileKey = msg.Document.FileID
		incoming.FileName = msg.Document.FileName
		incoming.FileSize = msg.Document.FileSize
	}

	// Handle photo (use the largest photo)
	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		incoming.MessageType = im.MessageTypeImage
		incoming.FileKey = largest.FileID
		incoming.FileName = "photo.jpg"
		incoming.FileSize = int64(largest.FileSize)
	}

	return incoming
}

// resolveChatID returns ChatID if set, otherwise falls back to UserID (for direct messages).
func resolveChatID(incoming *im.IncomingMessage) string {
	if incoming.ChatID != "" {
		return incoming.ChatID
	}
	return incoming.UserID
}

// ── Send reply ──

func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	chatID := resolveChatID(incoming)
	text := transformThinkBlocks(reply.Content)

	body := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if incoming.ThreadID != "" {
		if tid, err := strconv.Atoi(incoming.ThreadID); err == nil {
			body["message_thread_id"] = tid
		}
	}
	return a.callAPI(ctx, "sendMessage", body)
}

func (a *Adapter) sendMessage(ctx context.Context, chatID, text, replyToMessageID string) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if replyToMessageID != "" {
		body["reply_to_message_id"] = replyToMessageID
	}

	return a.callAPI(ctx, "sendMessage", body)
}

func (a *Adapter) editMessage(ctx context.Context, chatID, messageID, text, parseMode string) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": json.Number(messageID),
		"text":       text,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	return a.callAPI(ctx, "editMessageText", body)
}

// httpClient is a shared HTTP client with a reasonable timeout for Telegram API calls.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// callAPI calls the Telegram Bot API, discarding the result.
func (a *Adapter) callAPI(ctx context.Context, method string, body interface{}) error {
	return a.callAPIWithResult(ctx, method, body, nil)
}

// callAPIWithResult calls the Telegram Bot API and optionally decodes the result field.
func (a *Adapter) callAPIWithResult(ctx context.Context, method string, body interface{}, result interface{}) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", a.botToken, method)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var apiResp struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !apiResp.OK {
		return fmt.Errorf("telegram API %s failed: %s", method, string(apiResp.Result))
	}
	if result != nil {
		if err := json.Unmarshal(apiResp.Result, result); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// ── StreamSender implementation (edit message in-place) ──

// minEditInterval is the minimum time between consecutive editMessageText calls
// to avoid hitting Telegram's rate limit (~30 msg/sec global, ~20 edit/min per chat).
const minEditInterval = 500 * time.Millisecond

type streamState struct {
	mu        sync.Mutex
	content   strings.Builder
	msgID     string // Telegram message ID of the "thinking" message
	chatID    string
	lastEdit  time.Time // last successful editMessageText timestamp
	createdAt time.Time // for orphan stream detection
}

const (
	streamOrphanTTL      = 5 * time.Minute
	streamReaperInterval = 1 * time.Minute
)

var (
	streamsMu       sync.Mutex
	streams         = map[string]*streamState{}
	startReaperOnce sync.Once
	reaperStopCh    = make(chan struct{})
)

// startStreamReaper starts a background goroutine (once) that periodically
// removes orphaned stream entries. This prevents memory leaks when EndStream
// is never called due to panics or pipeline errors.
func startStreamReaper() {
	startReaperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(streamReaperInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					cutoff := time.Now().Add(-streamOrphanTTL)
					streamsMu.Lock()
					for id, state := range streams {
						if state.createdAt.Before(cutoff) {
							delete(streams, id)
						}
					}
					streamsMu.Unlock()
				case <-reaperStopCh:
					return
				}
			}
		}()
	})
}

func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	chatID := resolveChatID(incoming)

	// Send initial "thinking" message
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    "正在思考...",
	}
	if incoming.ThreadID != "" {
		if tid, err := strconv.Atoi(incoming.ThreadID); err == nil {
			body["message_thread_id"] = tid
		}
	}
	var sentMsg struct {
		MessageID int `json:"message_id"`
	}
	if err := a.callAPIWithResult(ctx, "sendMessage", body, &sentMsg); err != nil {
		return "", fmt.Errorf("telegram start stream: %w", err)
	}

	msgID := fmt.Sprintf("%d", sentMsg.MessageID)
	streamID := fmt.Sprintf("%s:%s", chatID, msgID)

	streamsMu.Lock()
	streams[streamID] = &streamState{
		msgID:     msgID,
		chatID:    chatID,
		createdAt: time.Now(),
	}
	streamsMu.Unlock()

	logger.Infof(ctx, "[Telegram] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

func (a *Adapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	if content == "" {
		return nil
	}

	streamsMu.Lock()
	state, ok := streams[streamID]
	streamsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	state.content.WriteString(content)

	// Throttle: skip edit if last edit was too recent
	if time.Since(state.lastEdit) < minEditInterval {
		state.mu.Unlock()
		return nil
	}

	fullContent := transformThinkBlocks(state.content.String())
	state.lastEdit = time.Now()
	state.mu.Unlock()

	if err := a.editMessage(ctx, state.chatID, state.msgID, fullContent, ""); err != nil {
		logger.Warnf(ctx, "[Telegram] Failed to update stream chunk: %v", err)
	}

	return nil
}

func (a *Adapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	streamsMu.Lock()
	state, ok := streams[streamID]
	delete(streams, streamID)
	streamsMu.Unlock()

	if !ok {
		return nil
	}

	state.mu.Lock()
	fullContent := transformThinkBlocks(state.content.String())
	state.mu.Unlock()

	if err := a.editMessage(ctx, state.chatID, state.msgID, fullContent, "Markdown"); err != nil {
		logger.Warnf(ctx, "[Telegram] Failed to end stream: %v", err)
	}

	logger.Infof(ctx, "[Telegram] Streaming ended: stream_id=%s", streamID)
	return nil
}

func transformThinkBlocks(content string) string {
	return im.TransformThinkBlocks(content, im.TelegramThinkStyle)
}

// ── FileDownloader implementation ──

func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("file_key is required")
	}

	// Get file path via getFile API
	var fileInfo struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := a.callAPIWithResult(ctx, "getFile", map[string]string{"file_id": msg.FileKey}, &fileInfo); err != nil {
		return nil, "", fmt.Errorf("get file info: %w", err)
	}

	// Download the file
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", a.botToken, fileInfo.FilePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return resp.Body, msg.FileName, nil
}
