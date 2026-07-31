package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// MessageHandler is called when an IM message is received via long connection.
type MessageHandler func(ctx context.Context, msg *im.IncomingMessage) error

// LongConnClient manages a Feishu/Lark WebSocket long connection.
type LongConnClient struct {
	region   Region
	appID    string
	wsClient *larkws.Client
}

// NewLongConnClient creates a long connection client on the given region's cloud.
// When a message arrives, it converts it to IncomingMessage and calls handler.
func NewLongConnClient(region Region, appID, appSecret string, handler MessageHandler) *LongConnClient {
	// Long connection mode does not require verificationToken or encryptKey;
	// those are only used for webhook signature verification and decryption.
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			msg := convertEvent(region, event)
			if msg == nil {
				return nil
			}
			return handler(ctx, msg)
		})

	sdkLogger := &feishuLoggerAdapter{region: region, appID: appID}

	// WithDomain points the SDK at the region's cloud. It defaults to
	// open.feishu.cn, so Lark apps would otherwise fail to authenticate.
	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
		larkws.WithLogger(sdkLogger),
		larkws.WithDomain(region.OpenBaseURL),
	)

	return &LongConnClient{region: region, appID: appID, wsClient: wsClient}
}

// Start begins the WebSocket long connection. It blocks until ctx is cancelled.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] %s WebSocket connecting (app_id=%s)...", c.region.Label, c.appID)
	return c.wsClient.Start(ctx)
}

// Close tears down the WebSocket long connection.
//
// This is the only reliable way to stop a long connection: the SDK's
// Start blocks on a bare select{} and neither Start, pingLoop, nor
// receiveMessageLoop observe the passed context, so cancelling ctx alone leaves
// the underlying socket alive and the SDK auto-reconnecting. Client.Close()
// (added in oapi-sdk-go v3.9.7) flips autoReconnect off and calls disconnect,
// which actually closes the socket. Callers should still cancel the start ctx
// as a belt-and-braces fallback for the start goroutine.
func (c *LongConnClient) Close() {
	c.wsClient.Close()
}

// feishuLoggerAdapter bridges the Feishu/Lark SDK logger to our unified logger,
// replacing raw SDK connection messages with a consistent format.
type feishuLoggerAdapter struct {
	region Region
	appID  string
}

func (l *feishuLoggerAdapter) Debug(ctx context.Context, args ...interface{}) {
	logger.Debugf(ctx, "[%s] %s", l.region.Label, fmt.Sprint(args...))
}

func (l *feishuLoggerAdapter) Info(ctx context.Context, args ...interface{}) {
	msg := fmt.Sprint(args...)
	if strings.HasPrefix(msg, "connected to ") {
		logger.Infof(ctx, "[IM] %s WebSocket connected successfully (app_id=%s)", l.region.Label, l.appID)
		return
	}
	logger.Infof(ctx, "[%s] %s", l.region.Label, msg)
}

func (l *feishuLoggerAdapter) Warn(ctx context.Context, args ...interface{}) {
	logger.Warnf(ctx, "[%s] %s", l.region.Label, fmt.Sprint(args...))
}

func (l *feishuLoggerAdapter) Error(ctx context.Context, args ...interface{}) {
	logger.Errorf(ctx, "[%s] %s", l.region.Label, fmt.Sprint(args...))
}

// convertEvent converts a Feishu/Lark SDK event to a unified IncomingMessage.
// Supports text, file, image and post messages. Returns nil for other types.
func convertEvent(region Region, event *larkim.P2MessageReceiveV1) *im.IncomingMessage {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		logger.Warnf(context.Background(), "[%s][RX] event dropped: nil event/event/message", region.Label)
		return nil
	}

	msg := event.Event.Message

	// Debug: log every raw receive event so we can see exactly what the platform
	// delivers (or doesn't). This is the single chokepoint for all message
	// types — adding it here catches text/file/image/post uniformly.
	logger.Infof(context.Background(),
		"[%s][RX] msg_type=%q chat_type=%q chat_id=%q msg_id=%q root_id=%q parent_id=%q thread_id=%q content=%q",
		region.Label,
		ptrStr(msg.MessageType), ptrStr(msg.ChatType), ptrStr(msg.ChatId),
		ptrStr(msg.MessageId), ptrStr(msg.RootId), ptrStr(msg.ParentId),
		ptrStr(msg.ThreadId), ptrStr(msg.Content),
	)

	if msg.MessageType == nil {
		return nil
	}

	msgType := *msg.MessageType

	// Sender info
	openID := ""
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil && event.Event.Sender.SenderId.OpenId != nil {
		openID = *event.Event.Sender.SenderId.OpenId
	}

	// Chat type
	chatType := im.ChatTypeDirect
	chatID := ""
	if msg.ChatType != nil && *msg.ChatType == "group" {
		chatType = im.ChatTypeGroup
		if msg.ChatId != nil {
			chatID = *msg.ChatId
		}
	}

	// Message ID
	messageID := ""
	if msg.MessageId != nil {
		messageID = *msg.MessageId
	}

	switch msgType {
	case "text":
		return convertTextEvent(region, msg, openID, chatID, chatType, messageID)
	case "file":
		return convertFileEvent(region, msg, openID, chatID, chatType, messageID)
	case "image":
		return convertImageEvent(region, msg, openID, chatID, chatType, messageID)
	case "post":
		return convertPostEvent(region, msg, openID, chatID, chatType, messageID)
	default:
		return nil
	}
}

// convertTextEvent handles text message type.
func convertTextEvent(
	region Region, msg *larkim.EventMessage,
	openID, chatID string, chatType im.ChatType, messageID string,
) *im.IncomingMessage {
	var textContent struct {
		Text string `json:"text"`
	}
	if msg.Content == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(*msg.Content), &textContent); err != nil {
		return nil
	}

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
		Platform:    region.Platform,
		MessageType: im.MessageTypeText,
		UserID:      openID,
		ChatID:      chatID,
		ChatType:    chatType,
		Content:     strings.TrimSpace(content),
		MessageID:   messageID,
	}
}

// convertFileEvent handles file message type.
func convertFileEvent(
	region Region, msg *larkim.EventMessage,
	openID, chatID string, chatType im.ChatType, messageID string,
) *im.IncomingMessage {
	if msg.Content == nil {
		return nil
	}
	var fileContent struct {
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
	}
	if err := json.Unmarshal([]byte(*msg.Content), &fileContent); err != nil {
		return nil
	}
	if fileContent.FileKey == "" {
		return nil
	}

	return &im.IncomingMessage{
		Platform:    region.Platform,
		MessageType: im.MessageTypeFile,
		UserID:      openID,
		ChatID:      chatID,
		ChatType:    chatType,
		MessageID:   messageID,
		FileKey:     fileContent.FileKey,
		FileName:    fileContent.FileName,
	}
}

// convertImageEvent handles image message type.
// Downloads via GetMessageResource API with type=image.
func convertImageEvent(
	region Region, msg *larkim.EventMessage,
	openID, chatID string, chatType im.ChatType, messageID string,
) *im.IncomingMessage {
	if msg.Content == nil {
		return nil
	}
	var imageContent struct {
		ImageKey string `json:"image_key"`
	}
	if err := json.Unmarshal([]byte(*msg.Content), &imageContent); err != nil {
		return nil
	}
	if imageContent.ImageKey == "" {
		return nil
	}

	return &im.IncomingMessage{
		Platform:    region.Platform,
		MessageType: im.MessageTypeImage,
		UserID:      openID,
		ChatID:      chatID,
		ChatType:    chatType,
		MessageID:   messageID,
		FileKey:     imageContent.ImageKey,
		FileName:    imageContent.ImageKey + ".png",
	}
}

// convertPostEvent handles rich-text (post) message type.
// Extracts all plain text content and treats it as a text query for QA.
func convertPostEvent(
	region Region, msg *larkim.EventMessage,
	openID, chatID string, chatType im.ChatType, messageID string,
) *im.IncomingMessage {
	if msg.Content == nil {
		return nil
	}

	// Post content structure: {"title":"...", "content":[[{"tag":"text","text":"..."},{"tag":"a","href":"...","text":"..."}]]}
	var postContent struct {
		Title   string              `json:"title"`
		Content [][]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(*msg.Content), &postContent); err != nil {
		return nil
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
			case "at":
				// Skip @mentions
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
		return nil
	}

	return &im.IncomingMessage{
		Platform:    region.Platform,
		MessageType: im.MessageTypeText,
		UserID:      openID,
		ChatID:      chatID,
		ChatType:    chatType,
		Content:     content,
		MessageID:   messageID,
	}
}

// ptrStr safely dereferences a *string for logging; returns "" for nil so the
// log line stays readable instead of printing <nil>.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
