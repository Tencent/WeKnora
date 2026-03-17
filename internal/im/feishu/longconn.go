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

// LongConnClient manages a Feishu WebSocket long connection.
type LongConnClient struct {
	appID    string
	wsClient *larkws.Client
}

// NewLongConnClient creates a Feishu long connection client.
// When a text message arrives, it converts it to IncomingMessage and calls handler.
func NewLongConnClient(appID, appSecret string, handler MessageHandler) *LongConnClient {
	// Long connection mode does not require verificationToken or encryptKey;
	// those are only used for webhook signature verification and decryption.
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			msg := convertEvent(event)
			if msg == nil {
				return nil
			}
			return handler(ctx, msg)
		})

	sdkLogger := &feishuLoggerAdapter{appID: appID}

	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
		larkws.WithLogger(sdkLogger),
	)

	return &LongConnClient{appID: appID, wsClient: wsClient}
}

// Start begins the WebSocket long connection. It blocks until ctx is cancelled.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] Feishu WebSocket connecting (app_id=%s)...", c.appID)
	return c.wsClient.Start(ctx)
}

// feishuLoggerAdapter bridges the Feishu SDK logger to our unified logger,
// replacing raw SDK connection messages with a consistent format.
type feishuLoggerAdapter struct {
	appID string
}

func (l *feishuLoggerAdapter) Debug(ctx context.Context, args ...interface{}) {
	logger.Debugf(ctx, "[Feishu] %s", fmt.Sprint(args...))
}

func (l *feishuLoggerAdapter) Info(ctx context.Context, args ...interface{}) {
	msg := fmt.Sprint(args...)
	if strings.HasPrefix(msg, "connected to ") {
		logger.Infof(ctx, "[IM] Feishu WebSocket connected successfully (app_id=%s)", l.appID)
		return
	}
	logger.Infof(ctx, "[Feishu] %s", msg)
}

func (l *feishuLoggerAdapter) Warn(ctx context.Context, args ...interface{}) {
	logger.Warnf(ctx, "[Feishu] %s", fmt.Sprint(args...))
}

func (l *feishuLoggerAdapter) Error(ctx context.Context, args ...interface{}) {
	logger.Errorf(ctx, "[Feishu] %s", fmt.Sprint(args...))
}

// convertEvent converts a Feishu SDK event to a unified IncomingMessage.
// Supports text and file messages. Returns nil for unsupported types.
func convertEvent(event *larkim.P2MessageReceiveV1) *im.IncomingMessage {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}

	msg := event.Event.Message
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
		return convertTextEvent(msg, openID, chatID, chatType, messageID)
	case "file":
		return convertFileEvent(msg, openID, chatID, chatType, messageID)
	case "image":
		return convertImageEvent(msg, openID, chatID, chatType, messageID)
	case "post":
		return convertPostEvent(msg, openID, chatID, chatType, messageID)
	default:
		return nil
	}
}

// convertTextEvent handles text message type.
func convertTextEvent(msg *larkim.EventMessage, openID, chatID string, chatType im.ChatType, messageID string) *im.IncomingMessage {
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
		Platform:    im.PlatformFeishu,
		MessageType: im.MessageTypeText,
		UserID:      openID,
		ChatID:      chatID,
		ChatType:    chatType,
		Content:     strings.TrimSpace(content),
		MessageID:   messageID,
	}
}

// convertFileEvent handles file message type.
func convertFileEvent(msg *larkim.EventMessage, openID, chatID string, chatType im.ChatType, messageID string) *im.IncomingMessage {
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
		Platform:    im.PlatformFeishu,
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
func convertImageEvent(msg *larkim.EventMessage, openID, chatID string, chatType im.ChatType, messageID string) *im.IncomingMessage {
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
		Platform:    im.PlatformFeishu,
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
func convertPostEvent(msg *larkim.EventMessage, openID, chatID string, chatType im.ChatType, messageID string) *im.IncomingMessage {
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
		Platform:    im.PlatformFeishu,
		MessageType: im.MessageTypeText,
		UserID:      openID,
		ChatID:      chatID,
		ChatType:    chatType,
		Content:     content,
		MessageID:   messageID,
	}
}
