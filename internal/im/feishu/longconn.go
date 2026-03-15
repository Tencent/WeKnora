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
// Returns nil for non-text messages.
func convertEvent(event *larkim.P2MessageReceiveV1) *im.IncomingMessage {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}

	msg := event.Event.Message
	if msg.MessageType == nil || *msg.MessageType != "text" {
		return nil
	}

	// Parse text content from JSON: {"text": "..."}
	var textContent struct {
		Text string `json:"text"`
	}
	if msg.Content == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(*msg.Content), &textContent); err != nil {
		return nil
	}

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
		Platform:  im.PlatformFeishu,
		UserID:    openID,
		ChatID:    chatID,
		ChatType:  chatType,
		Content:   strings.TrimSpace(content),
		MessageID: messageID,
	}
}
