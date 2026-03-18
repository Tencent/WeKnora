package slack

import (
	"context"
	"encoding/json"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// MessageHandler is called when an IM message is received via long connection.
type MessageHandler func(ctx context.Context, msg *im.IncomingMessage) error

// LongConnClient manages a Slack Socket Mode long connection.
type LongConnClient struct {
	appToken string
	botToken string
	handler  MessageHandler

	api    *slack.Client
	client *socketmode.Client
}

// NewLongConnClient creates a Slack long connection client.
func NewLongConnClient(appToken, botToken string, handler MessageHandler) *LongConnClient {
	api := slack.New(
		botToken,
		slack.OptionAppLevelToken(appToken),
	)

	client := socketmode.New(
		api,
		socketmode.OptionDebug(false), // true for debugging
	)

	return &LongConnClient{
		appToken: appToken,
		botToken: botToken,
		handler:  handler,
		api:      api,
		client:   client,
	}
}

// GetAPI returns the underlying slack API client.
func (c *LongConnClient) GetAPI() *slack.Client {
	return c.api
}

// Start begins the WebSocket long connection. It blocks until ctx is cancelled.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] Slack WebSocket connecting...")

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt := <-c.client.Events:
				switch evt.Type {
				case socketmode.EventTypeConnecting:
					logger.Infof(ctx, "[Slack] Connecting to Slack with Socket Mode...")
				case socketmode.EventTypeConnectionError:
					logger.Errorf(ctx, "[Slack] Connection failed. Retrying later...")
				case socketmode.EventTypeConnected:
					logger.Infof(ctx, "[IM] Slack WebSocket connected successfully")
				case socketmode.EventTypeEventsAPI:
					eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
					if !ok {
						logger.Warnf(ctx, "[Slack] Ignored %+v", evt)
						continue
					}

					// Acknowledge the event
					c.client.Ack(*evt.Request)

					// Handle the event
					c.handleEvent(ctx, eventsAPIEvent, evt.Request.Payload)
				}
			}
		}
	}()

	return c.client.RunContext(ctx)
}

func (c *LongConnClient) handleEvent(ctx context.Context, eventsAPIEvent slackevents.EventsAPIEvent, rawPayload json.RawMessage) {
	logger.Infof(ctx, "[Slack] Received event type: %s", eventsAPIEvent.Type)
	switch eventsAPIEvent.Type {
	case slackevents.CallbackEvent:
		var rawEvent struct {
			Event struct {
				Files []slack.File `json:"files"`
			} `json:"event"`
		}
		_ = json.Unmarshal(rawPayload, &rawEvent)
		files := rawEvent.Event.Files

		innerEvent := eventsAPIEvent.InnerEvent
		logger.Infof(ctx, "[Slack] Received inner event type: %s", innerEvent.Type)
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			logger.Infof(ctx, "[Slack] AppMentionEvent: user=%s channel=%s text=%s", ev.User, ev.Channel, ev.Text)
			// Handle @bot mention in a channel
			threadTs := ev.ThreadTimeStamp
			if threadTs == "" {
				threadTs = ev.TimeStamp
			}
			c.processMessage(ctx, ev.User, ev.Channel, ev.Text, threadTs, im.ChatTypeGroup, files)
		case *slackevents.MessageEvent:
			logger.Infof(ctx, "[Slack] MessageEvent: user=%s channel=%s text=%s subtype=%s bot_id=%s", ev.User, ev.Channel, ev.Text, ev.SubType, ev.BotID)
			if ev.BotID != "" {
				return
			}
			if ev.SubType != "" && ev.SubType != "file_share" {
				return
			}

			chatType := im.ChatTypeDirect
			if ev.ChannelType == "channel" || ev.ChannelType == "group" {
				chatType = im.ChatTypeGroup
			}

			threadTs := ev.ThreadTimeStamp
			if threadTs == "" {
				threadTs = ev.TimeStamp
			}

			c.processMessage(ctx, ev.User, ev.Channel, ev.Text, threadTs, chatType, files)
		default:
			logger.Warnf(ctx, "[Slack] Unhandled inner event type: %T", innerEvent.Data)
		}
	default:
		logger.Warnf(ctx, "[Slack] Unhandled event type: %s", eventsAPIEvent.Type)
	}
}

func (c *LongConnClient) processMessage(ctx context.Context, user, channel, text, ts string, chatType im.ChatType, files []slack.File) {
	incoming := parseIncomingMessage(user, channel, text, ts, chatType, files)

	if err := c.handler(ctx, incoming); err != nil {
		logger.Errorf(ctx, "[Slack] Handle message error: %v", err)
	}
}
