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
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// Compile-time check that Adapter implements im.StreamSender and im.FileDownloader.
var _ im.StreamSender = (*Adapter)(nil)
var _ im.FileDownloader = (*Adapter)(nil)

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
	startStreamReaper()
	return &Adapter{
		appID:             appID,
		appSecret:         appSecret,
		verificationToken: verificationToken,
		encryptKey:        encryptKey,
	}
}

// startStreamReaper starts a background goroutine (once) that periodically
// removes orphaned stream entries from feishuStreams. This prevents memory
// leaks when EndStream is never called due to panics or pipeline errors.
func startStreamReaper() {
	startReaperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(streamReaperInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					cutoff := time.Now().Add(-streamOrphanTTL)
					feishuStreamsMu.Lock()
					for id, state := range feishuStreams {
						if state.createdAt.Before(cutoff) {
							delete(feishuStreams, id)
						}
					}
					feishuStreamsMu.Unlock()
				case <-reaperStopCh:
					return
				}
			}
		}()
	})
}

// StopStreamReaper stops the background stream reaper goroutine.
// Should be called during application shutdown.
func StopStreamReaper() {
	select {
	case <-reaperStopCh:
		// already closed
	default:
		close(reaperStopCh)
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
	RootID      string `json:"root_id"`
	ParentID    string `json:"parent_id"`
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

	// Compute thread ID: use root_id for threaded replies, or message_id for top-level messages.
	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
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

	switch msg.MessageType {
	case "text":
		// Parse text content
		var textContent struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &textContent); err != nil {
			return nil, fmt.Errorf("unmarshal text content: %w", err)
		}

		// Strip @bot mention from group messages
		content := textContent.Text
		if chatType == im.ChatTypeGroup {
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
			Platform:    im.PlatformFeishu,
			MessageType: im.MessageTypeText,
			UserID:      openID,
			ChatID:      chatID,
			ChatType:    chatType,
			Content:     strings.TrimSpace(content),
			MessageID:   msg.MessageID,
			ThreadID:    threadID,
		}, nil

	case "file":
		var fileContent struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &fileContent); err != nil {
			return nil, fmt.Errorf("unmarshal file content: %w", err)
		}
		if fileContent.FileKey == "" {
			return nil, nil
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformFeishu,
			MessageType: im.MessageTypeFile,
			UserID:      openID,
			ChatID:      chatID,
			ChatType:    chatType,
			MessageID:   msg.MessageID,
			ThreadID:    threadID,
			FileKey:     fileContent.FileKey,
			FileName:    fileContent.FileName,
		}, nil

	case "image":
		var imageContent struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &imageContent); err != nil {
			return nil, fmt.Errorf("unmarshal image content: %w", err)
		}
		if imageContent.ImageKey == "" {
			return nil, nil
		}
		return &im.IncomingMessage{
			Platform:    im.PlatformFeishu,
			MessageType: im.MessageTypeImage,
			UserID:      openID,
			ChatID:      chatID,
			ChatType:    chatType,
			MessageID:   msg.MessageID,
			ThreadID:    threadID,
			FileKey:     imageContent.ImageKey,
			FileName:    imageContent.ImageKey + ".png",
		}, nil

	case "post":
		// Rich text: extract plain text for QA
		var postContent struct {
			Title   string              `json:"title"`
			Content [][]json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &postContent); err != nil {
			return nil, fmt.Errorf("unmarshal post content: %w", err)
		}

		var textParts []string
		if postContent.Title != "" {
			textParts = append(textParts, postContent.Title)
		}
		for _, line := range postContent.Content {
			var lineText strings.Builder
			for _, elem := range line {
				var tag struct {
					Tag  string `json:"tag"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(elem, &tag); err != nil {
					continue
				}
				switch tag.Tag {
				case "text", "a":
					lineText.WriteString(tag.Text)
				}
			}
			if t := strings.TrimSpace(lineText.String()); t != "" {
				textParts = append(textParts, t)
			}
		}

		content := strings.Join(textParts, "\n")
		if chatType == im.ChatTypeGroup {
			for strings.HasPrefix(content, "@_user_") {
				idx := strings.Index(content, " ")
				if idx >= 0 {
					content = content[idx+1:]
				} else {
					break
				}
			}
		}
		content = strings.TrimSpace(content)
		if content == "" {
			return nil, nil
		}

		return &im.IncomingMessage{
			Platform:    im.PlatformFeishu,
			MessageType: im.MessageTypeText,
			UserID:      openID,
			ChatID:      chatID,
			ChatType:    chatType,
			Content:     content,
			MessageID:   msg.MessageID,
			ThreadID:    threadID,
		}, nil

	default:
		logger.Infof(c.Request.Context(), "[Feishu] Ignoring unsupported message type: %s", msg.MessageType)
		return nil, nil
	}
}

// SendReply sends a reply message via Feishu API.
//
// It uses the "reply message" API (POST /im/v1/messages/:message_id/reply) so the
// reply lands under the original message / thread instead of creating a new
// top-level message (which, in topic-enabled groups, would spawn a new topic).
// Feishu automatically replies in thread form when the replied-to message is
// already a thread message, so we leave reply_in_thread at its default (false).
//
// If the reply API fails with a fallback-eligible code (e.g. 230071 group does
// not support reply-in-thread, 230019 topic gone, 230054 unsupported message
// type), we retry once via the plain "send message" API so the reply still
// reaches the user.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	accessToken, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// Build text message content
	content, _ := json.Marshal(map[string]string{"text": reply.Content})

	// Reply payload (no receive_id — the path message_id locates the chat)
	replyPayload := map[string]interface{}{
		"msg_type": "text",
		"content":  string(content),
	}

	// Fallback payload (plain send-message API) — needs receive_id
	receiveIDType, receiveID := a.resolveReceiveID(incoming)
	fallbackPayload := map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	}

	return a.sendWithFallback(ctx, accessToken, incoming, replyPayload, fallbackPayload, receiveIDType)
}

// resolveReceiveID computes the (receive_id_type, receive_id) pair used by the
// plain "send message" API. Group chats target chat_id; direct chats target the
// user's open_id. Used both for the fallback path and for legacy callers.
func (a *Adapter) resolveReceiveID(incoming *im.IncomingMessage) (receiveIDType string, receiveID string) {
	receiveIDType = "open_id"
	receiveID = incoming.UserID
	if incoming.ChatType == im.ChatTypeGroup && incoming.ChatID != "" {
		receiveIDType = "chat_id"
		receiveID = incoming.ChatID
	}
	return
}

// fallbackEligibleErrorCodes are Feishu API error codes for which replying via
// the reply-message API cannot work (e.g. the group does not support threads,
// the topic was deleted, the message type is unsupported). In these cases we
// retry once via the plain send-message API so the reply still reaches the user.
var fallbackEligibleErrorCodes = map[int]bool{
	230019: true, // The topic does NOT exist.
	230054: true, // This operation is not supported for this message type.
	230071: true, // The group to which the message belongs does not support reply in thread.
}

// sendWithFallback sends a message via the reply-message API first
// (POST /im/v1/messages/:message_id/reply), and on a fallback-eligible error
// retries once via the plain send-message API (POST /im/v1/messages).
//
//   - replyPayload   — body for the reply API (no receive_id needed)
//   - fallbackPayload — body for the send API (includes receive_id)
//   - receiveIDType   — receive_id_type query param for the fallback send API
func (a *Adapter) sendWithFallback(
	ctx context.Context,
	accessToken string,
	incoming *im.IncomingMessage,
	replyPayload, fallbackPayload map[string]interface{},
	receiveIDType string,
) error {
	// If we have a usable message_id, try the reply API first so the reply
	// lands under the original message / thread. If message_id is empty (rare:
	// some event payloads omit it), skip straight to the fallback send API —
	// the reply API needs a message_id in the path and can't work without one.
	if incoming.MessageID != "" && feishuSafePathParam(incoming.MessageID) {
		replyURL := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/reply", incoming.MessageID)
		code, msg, err := a.postFeishuMessage(ctx, accessToken, replyURL, replyPayload)
		switch {
		case err != nil:
			// Network/transport error — try the fallback since send-message is
			// a different endpoint that might succeed.
			logger.Warnf(ctx, "[Feishu] reply API transport error (will try fallback): %v", err)
		case code == 0:
			return nil
		case !fallbackEligibleErrorCodes[code]:
			return fmt.Errorf("feishu reply api error: code=%d msg=%s", code, msg)
		default:
			// Fallback-eligible: retry via plain send-message API.
			logger.Warnf(ctx, "[Feishu] reply API returned code=%d msg=%s, falling back to send-message API", code, msg)
		}
	} else if incoming.MessageID != "" {
		// message_id present but contains unsafe characters — refuse rather
		// than put it in a URL path. Don't fall back either, since a malformed
		// id suggests a tampered payload.
		return fmt.Errorf("invalid message_id for reply API: %q", incoming.MessageID)
	} else {
		logger.Warnf(ctx, "[Feishu] incoming message has no message_id; replying via send-message API (will not attach to thread)")
	}

	// Fallback: plain send-message API.
	fallbackURL := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType)
	code, msg, err := a.postFeishuMessage(ctx, accessToken, fallbackURL, fallbackPayload)
	if err != nil {
		return fmt.Errorf("send message (fallback): %w", err)
	}
	if code != 0 {
		return fmt.Errorf("feishu send api error: code=%d msg=%s", code, msg)
	}
	return nil
}

// postFeishuMessage POSTs a JSON payload to a Feishu IM message API and returns
// the (code, msg) from the response body. Shared by reply and send paths.
func (a *Adapter) postFeishuMessage(ctx context.Context, accessToken, url string, payload map[string]interface{}) (code int, msg string, err error) {
	payloadBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return 0, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", fmt.Errorf("decode response: %w", err)
	}
	return result.Code, result.Msg, nil
}

// ──────────────────────────────────────────────────────────────────────
// File download support via Feishu GetMessageResource API
// ──────────────────────────────────────────────────────────────────────

// feishuSafePathParam checks that a Feishu API path parameter contains only
// safe characters (alphanumeric, hyphen, underscore). This prevents path
// traversal attacks via crafted callback payloads.
func feishuSafePathParam(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return len(s) > 0
}

// DownloadFile downloads a file or image attachment from a Feishu message.
// Uses the GetMessageResource API: GET /open-apis/im/v1/messages/:message_id/resources/:file_key?type={file|image}
func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" || msg.MessageID == "" {
		return nil, "", fmt.Errorf("file_key and message_id are required")
	}

	// SSRF/path-traversal protection: validate path parameters contain only safe characters
	if !feishuSafePathParam(msg.MessageID) || !feishuSafePathParam(msg.FileKey) {
		return nil, "", fmt.Errorf("invalid message_id or file_key format")
	}

	accessToken, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get access token: %w", err)
	}

	// Determine resource type based on message type
	resourceType := "file"
	if msg.MessageType == im.MessageTypeImage {
		resourceType = "image"
	}

	apiURL := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/resources/%s?type=%s",
		msg.MessageID, msg.FileKey, resourceType)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download file: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("download file failed: status=%d", resp.StatusCode)
	}

	// Use the original file name from the message, or extract from Content-Disposition
	fileName := msg.FileName
	if fileName == "" {
		if cd := resp.Header.Get("Content-Disposition"); cd != "" {
			if idx := strings.Index(cd, "filename="); idx >= 0 {
				fileName = strings.Trim(cd[idx+len("filename="):], "\" ")
			}
		}
	}
	if fileName == "" {
		fileName = msg.FileKey
	}

	return resp.Body, fileName, nil
}

// ──────────────────────────────────────────────────────────────────────
// Feishu CardKit v1 streaming implementation (official best practice)
//
// Flow:
//  1. POST  /cardkit/v1/cards                                      — create card entity
//  2. POST  /im/v1/messages  content={"type":"card","data":{"card_id":"…"}} — send card
//  3. PUT   /cardkit/v1/cards/{id}/elements/{eid}/content          — stream element content
//  4. PATCH /cardkit/v1/cards/{id}/settings                        — set streaming_mode=false
//
// Reference: https://github.com/larksuite/openclaw-lark (official Lark plugin)
//            https://open.feishu.cn/document/cardkit-v1/streaming-updates-openapi-overview
// ──────────────────────────────────────────────────────────────────────

const (
	// streamingElementID is the element_id used in the card JSON for streaming content.
	streamingElementID = "streaming_content"
)

// feishuStreamState tracks per-stream accumulated content.
type feishuStreamState struct {
	mu         sync.Mutex
	content    strings.Builder
	seq        int64     // strictly incrementing sequence for CardKit API
	createdAt  time.Time // for orphan stream detection
	firstChunk bool      // true after the first real content chunk clears the placeholder
}

const (
	// streamOrphanTTL is the maximum lifetime of a stream entry before it's
	// considered orphaned (e.g., EndStream was never called due to an error).
	streamOrphanTTL = 5 * time.Minute
	// streamReaperInterval is how often the reaper scans for orphaned streams.
	streamReaperInterval = 1 * time.Minute
)

var (
	feishuStreamsMu sync.Mutex
	feishuStreams   = map[string]*feishuStreamState{}

	startReaperOnce sync.Once
	reaperStopCh    = make(chan struct{})
)

func (s *feishuStreamState) nextSeq() int {
	s.seq++
	return int(s.seq)
}

// buildStreamingCardJSON builds a Card JSON 2.0 with streaming_mode enabled.
func buildStreamingCardJSON() string {
	card := map[string]interface{}{
		"schema": "2.0",
		"config": map[string]interface{}{
			"streaming_mode": true,
			"summary":        map[string]string{"content": "正在思考..."},
		},
		"header": map[string]interface{}{
			"template": "blue",
			"title":    map[string]string{"tag": "plain_text", "content": "WeKnora"},
		},
		"body": map[string]interface{}{
			"elements": []map[string]interface{}{
				{
					"tag":        "markdown",
					"content":    "💭 正在思考...",
					"text_size":  "normal",
					"element_id": streamingElementID,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

// StartStream creates a CardKit card entity, sends it as a message, and returns the card_id.
func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	accessToken, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("get access token: %w", err)
	}

	// 1. Create card entity via CardKit API
	cardJSON := buildStreamingCardJSON()
	cardID, err := a.cardkitCreate(ctx, accessToken, cardJSON)
	if err != nil {
		return "", fmt.Errorf("create card: %w", err)
	}

	// 2. Send the card as a message (content type="card")
	if err := a.sendCardByCardID(ctx, accessToken, incoming, cardID); err != nil {
		return "", fmt.Errorf("send card message: %w", err)
	}

	// 3. Track stream state
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{createdAt: time.Now()}
	feishuStreamsMu.Unlock()

	logger.Infof(ctx, "[Feishu] Streaming started: card_id=%s", cardID)
	return cardID, nil
}

// UpdateStreamContent replaces the card element with the full visible content so far.
func (a *Adapter) UpdateStreamContent(ctx context.Context, incoming *im.IncomingMessage, streamID string, fullContent string) error {
	if fullContent == "" {
		return nil
	}

	feishuStreamsMu.Lock()
	state, ok := feishuStreams[streamID]
	feishuStreamsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	if !state.firstChunk {
		state.firstChunk = true
	}
	state.content.Reset()
	state.content.WriteString(fullContent)
	seq := state.nextSeq()
	state.mu.Unlock()

	accessToken, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	// Feishu card markdown only accepts an uploaded image_key inside ![alt](...),
	// not an external HTTP/COS URL. Convert markdown image URLs to image_keys
	// (uploading on demand) so the card update does not fail with 200570.
	content := a.resolveMarkdownImages(ctx, accessToken, fullContent)

	return a.cardkitUpdateElement(ctx, accessToken, streamID, streamingElementID, content, seq)
}

// FinalizeStream replaces the card with answer-only content.
func (a *Adapter) FinalizeStream(ctx context.Context, incoming *im.IncomingMessage, streamID string, finalContent string) error {
	return a.UpdateStreamContent(ctx, incoming, streamID, finalContent)
}

// SendStreamChunk is an alias for UpdateStreamContent.
func (a *Adapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	return a.UpdateStreamContent(ctx, incoming, streamID, content)
}

// EndStream disables streaming_mode and cleans up state.
func (a *Adapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	feishuStreamsMu.Lock()
	state, ok := feishuStreams[streamID]
	delete(feishuStreams, streamID)
	feishuStreamsMu.Unlock()

	accessToken, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	var seq int
	if ok {
		state.mu.Lock()
		seq = state.nextSeq()
		state.mu.Unlock()
	}

	// Turn off streaming_mode to remove loading indicator
	if err := a.cardkitSetStreaming(ctx, accessToken, streamID, false, seq); err != nil {
		logger.Warnf(ctx, "[Feishu] Failed to disable streaming_mode: %v", err)
	}

	logger.Infof(ctx, "[Feishu] Streaming ended: card_id=%s", streamID)
	return nil
}

// ── CardKit v1 API helpers ──

// cardkitCreate creates a card entity and returns the card_id.
// POST /open-apis/cardkit/v1/cards
func (a *Adapter) cardkitCreate(ctx context.Context, accessToken, cardJSON string) (string, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "card_json",
		"data": cardJSON,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/cardkit/v1/cards", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var result struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode: %w (body: %s)", err, string(respBody))
	}
	if result.Code != 0 {
		return "", fmt.Errorf("code=%d msg=%s", result.Code, result.Msg)
	}

	var data struct {
		CardID string `json:"card_id"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil {
		return "", fmt.Errorf("parse card_id: %w (raw: %s)", err, string(result.Data))
	}
	return data.CardID, nil
}

// sendCardByCardID sends a card_id as an interactive message.
// POST /open-apis/im/v1/messages  with content={"type":"card","data":{"card_id":"…"}}
// sendCardByCardID sends a card_id as an interactive message via the reply
// message API so it lands under the original message / thread. Falls back to
// the plain send-message API on fallback-eligible errors (e.g. group does not
// support reply-in-thread).
func (a *Adapter) sendCardByCardID(ctx context.Context, accessToken string, incoming *im.IncomingMessage, cardID string) error {
	receiveIDType, receiveID := a.resolveReceiveID(incoming)

	// Key: type must be "card" (not "card_id")
	content, _ := json.Marshal(map[string]interface{}{
		"type": "card",
		"data": map[string]string{"card_id": cardID},
	})

	// Reply payload (no receive_id) and fallback payload (with receive_id)
	// share the same content/msg_type — only receive_id differs.
	replyPayload := map[string]interface{}{
		"msg_type": "interactive",
		"content":  string(content),
	}
	fallbackPayload := map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   "interactive",
		"content":    string(content),
	}

	return a.sendWithFallback(ctx, accessToken, incoming, replyPayload, fallbackPayload, receiveIDType)
}

// cardkitUpdateElement updates a card element's content for streaming.
// PUT /open-apis/cardkit/v1/cards/:card_id/elements/:element_id/content
func (a *Adapter) cardkitUpdateElement(ctx context.Context, accessToken, cardID, elementID, content string, sequence int) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"content":  content,
		"sequence": sequence,
	})

	apiURL := fmt.Sprintf("https://open.feishu.cn/open-apis/cardkit/v1/cards/%s/elements/%s/content",
		cardID, elementID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("update element error: code=%d msg=%s", result.Code, result.Msg)
	}
	return nil
}

// Card markdown image handling.
//
// Feishu card markdown images use the syntax ![hover_text](image_key) where the
// value inside the parentheses MUST be an image_key obtained from the Feishu
// "upload image" API. External HTTP/COS URLs are rejected with
// code=200570 "card contains invalid image keys". WeKnora's content pipeline
// rewrites provider:// storage URLs to signed HTTP URLs, so by the time content
// reaches the card we must download each referenced image and re-upload it to
// Feishu to obtain a usable image_key.

// feishuMarkdownImageRe matches a markdown image whose target is an http(s) URL.
var feishuMarkdownImageRe = regexp.MustCompile(`!\[([^\]]*)\]\((https?://[^)\s]+)\)`)

// feishuMaxImageBytes caps the download size of an image before uploading to
// Feishu (Feishu's limit is 10MB; keep a small margin).
const feishuMaxImageBytes = 10 << 20

var (
	feishuImageKeyMu    sync.Mutex
	feishuImageKeyCache = make(map[string]string)
	// feishuImageDownloadClient validates resolved IPs before connecting so that
	// downloading a URL embedded in model/knowledge content cannot be abused for
	// SSRF against internal services.
	feishuImageDownloadClient = utils.NewSSRFSafeHTTPClient(utils.DefaultSSRFSafeHTTPClientConfig())
)

// imageCacheKey normalizes a URL for caching by dropping the query string, so a
// COS/MinIO signed URL (whose signature changes every request) maps to a stable
// key across streaming updates of the same object.
func imageCacheKey(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}

// resolveMarkdownImages replaces the URL inside every ![alt](httpURL) with a
// Feishu image_key. On failure it degrades the image to a plain text link so the
// rest of the card still renders instead of failing the whole update.
func (a *Adapter) resolveMarkdownImages(ctx context.Context, accessToken, content string) string {
	if !strings.Contains(content, "![") {
		return content
	}
	return feishuMarkdownImageRe.ReplaceAllStringFunc(content, func(match string) string {
		sub := feishuMarkdownImageRe.FindStringSubmatch(match)
		alt, rawURL := sub[1], sub[2]
		imgKey, err := a.imageKeyForURL(ctx, accessToken, rawURL)
		if err != nil || imgKey == "" {
			logger.Warnf(ctx, "[Feishu] image upload failed, degrading to link: url=%s err=%v", rawURL, err)
			label := alt
			if label == "" {
				label = "图片"
			}
			return fmt.Sprintf("[%s](%s)", label, rawURL)
		}
		return fmt.Sprintf("![%s](%s)", alt, imgKey)
	})
}

// imageKeyForURL returns a Feishu image_key for the given URL, uploading it if
// not already cached.
func (a *Adapter) imageKeyForURL(ctx context.Context, accessToken, rawURL string) (string, error) {
	key := imageCacheKey(rawURL)

	feishuImageKeyMu.Lock()
	if v, ok := feishuImageKeyCache[key]; ok {
		feishuImageKeyMu.Unlock()
		return v, nil
	}
	feishuImageKeyMu.Unlock()

	imgKey, err := a.uploadImageFromURL(ctx, accessToken, rawURL)
	if err != nil {
		return "", err
	}

	feishuImageKeyMu.Lock()
	feishuImageKeyCache[key] = imgKey
	feishuImageKeyMu.Unlock()
	return imgKey, nil
}

// uploadImageFromURL downloads the image at rawURL and uploads it to Feishu,
// returning the resulting image_key.
// POST /open-apis/im/v1/images  (multipart: image_type=message, image=<binary>)
func (a *Adapter) uploadImageFromURL(ctx context.Context, accessToken, rawURL string) (string, error) {
	if err := utils.ValidateURLForSSRF(rawURL); err != nil {
		return "", fmt.Errorf("ssrf validation: %w", err)
	}

	imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	imgResp, err := feishuImageDownloadClient.Do(imgReq)
	if err != nil {
		return "", fmt.Errorf("download image: %w", err)
	}
	defer imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download image: status=%d", imgResp.StatusCode)
	}

	imgData, err := io.ReadAll(io.LimitReader(imgResp.Body, feishuMaxImageBytes+1))
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	if len(imgData) == 0 {
		return "", fmt.Errorf("empty image body")
	}
	if len(imgData) > feishuMaxImageBytes {
		return "", fmt.Errorf("image exceeds %d bytes", feishuMaxImageBytes)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("image_type", "message"); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("image", "image")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(imgData); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/im/v1/images", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageKey string `json:"image_key"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("upload image error: code=%d msg=%s", result.Code, result.Msg)
	}
	if result.Data.ImageKey == "" {
		return "", fmt.Errorf("upload image: empty image_key")
	}
	return result.Data.ImageKey, nil
}

// cardkitSetStreaming updates the card's streaming_mode setting.
// PATCH /open-apis/cardkit/v1/cards/:card_id/settings
func (a *Adapter) cardkitSetStreaming(ctx context.Context, accessToken, cardID string, streaming bool, sequence int) error {
	settings, _ := json.Marshal(map[string]interface{}{
		"streaming_mode": streaming,
	})
	payload, _ := json.Marshal(map[string]interface{}{
		"settings": string(settings),
		"sequence": sequence,
	})

	apiURL := fmt.Sprintf("https://open.feishu.cn/open-apis/cardkit/v1/cards/%s/settings", cardID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("set streaming error: code=%d msg=%s", result.Code, result.Msg)
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
