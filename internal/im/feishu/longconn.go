package feishu

import (
	"context"
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
// apiBaseURL overrides the SDK's bootstrap domain (empty uses the region
// default); set it to a reverse-proxy origin for private/internal deployments.
// When a message arrives, it converts it to IncomingMessage and calls handler.
func NewLongConnClient(adapter *Adapter, handler MessageHandler) *LongConnClient {
	region, appID, appSecret, apiBaseURL := adapter.region, adapter.appID, adapter.appSecret, adapter.apiBaseURL
	// Long connection mode does not require verificationToken or encryptKey;
	// those are only used for webhook signature verification and decryption.
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			msg, err := adapter.convertEvent(ctx, event)
			if err != nil {
				return err
			}
			if msg == nil {
				return nil
			}
			return handler(ctx, msg)
		})

	sdkLogger := &feishuLoggerAdapter{region: region, appID: appID}

	apiBaseURL = strings.TrimRight(strings.TrimSpace(apiBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = region.OpenBaseURL
	}

	// WithDomain points the SDK at the region's cloud (open.feishu.cn by
	// default). apiBaseURL overrides it for deployments that reach Feishu
	// through a reverse proxy.
	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithAutoReconnect(true),
		larkws.WithLogger(sdkLogger),
		larkws.WithDomain(apiBaseURL),
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

// convertEvent leaves thread-session semantics unchanged; only inbound material
// parsing and mention validation are shared with the webhook adapter.
func (a *Adapter) convertEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) (*im.IncomingMessage, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		logger.Warnf(context.Background(), "[%s][RX] event dropped: nil event/event/message", a.region.Label)
		return nil, nil
	}

	msg := event.Event.Message
	loggedContent := ptrStr(msg.Content)
	if ptrStr(msg.MessageType) == "interactive" {
		loggedContent = "[card material; body is read by the queue worker]"
	}

	// Debug: log every raw receive event so we can see exactly what the platform
	// delivers (or doesn't). This is the single chokepoint for all message
	// types — adding it here catches text/file/image/post uniformly.
	logger.Infof(context.Background(),
		"[%s][RX] msg_type=%q chat_type=%q chat_id=%q msg_id=%q root_id=%q parent_id=%q thread_id=%q content=%q",
		a.region.Label,
		ptrStr(msg.MessageType), ptrStr(msg.ChatType), ptrStr(msg.ChatId),
		ptrStr(msg.MessageId), ptrStr(msg.RootId), ptrStr(msg.ParentId),
		ptrStr(msg.ThreadId), loggedContent,
	)

	senderID, senderType := "", ""
	if event.Event.Sender != nil {
		senderType = ptrStr(event.Event.Sender.SenderType)
		if event.Event.Sender.SenderId != nil {
			senderID = ptrStr(event.Event.Sender.SenderId.OpenId)
		}
	}
	return a.parseIncoming(ctx, &feishuMessage{
		MessageID: ptrStr(msg.MessageId), ParentID: ptrStr(msg.ParentId), RootID: ptrStr(msg.RootId),
		MessageType: ptrStr(msg.MessageType), ChatType: ptrStr(msg.ChatType), ChatID: ptrStr(msg.ChatId),
		Content: ptrStr(msg.Content), CreateTime: ptrStr(msg.CreateTime), UpdateTime: ptrStr(msg.UpdateTime),
		SenderType: senderType, Mentions: msg.Mentions,
	}, senderID, "")
}

// ptrStr safely dereferences a *string for logging; returns "" for nil so the
// log line stays readable instead of printing <nil>.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
