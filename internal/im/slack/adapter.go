package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// Compile-time checks.
var (
	_ im.Adapter        = (*Adapter)(nil)
	_ im.StreamSender   = (*Adapter)(nil)
	_ im.FileDownloader = (*Adapter)(nil)
)

// Adapter implements im.Adapter and im.StreamSender for Slack.
// It delegates to the Slack LongConnClient for Socket Mode.
type Adapter struct {
	client        *LongConnClient
	api           *slack.Client
	signingSecret string
}

// NewAdapter creates an adapter backed by a Slack long connection client.
func NewAdapter(client *LongConnClient, api *slack.Client) *Adapter {
	return &Adapter{
		client: client,
		api:    api,
	}
}

// NewWebhookAdapter creates an adapter for Slack Events API via Webhook.
func NewWebhookAdapter(api *slack.Client, signingSecret string) *Adapter {
	return &Adapter{
		api:           api,
		signingSecret: signingSecret,
	}
}

func parseIncomingMessage(user, channel, text, ts string, chatType im.ChatType, files []slack.File) *im.IncomingMessage {
	content := text
	if chatType == im.ChatTypeGroup {
		// Slack mentions are in the format <@U12345678>
		for strings.HasPrefix(content, "<@") {
			idx := strings.Index(content, ">")
			if idx >= 0 {
				content = strings.TrimSpace(content[idx+1:])
			} else {
				break
			}
		}
	}

	msg := &im.IncomingMessage{
		Platform:  im.PlatformSlack,
		UserID:    user,
		ChatID:    channel,
		ChatType:  chatType,
		Content:   strings.TrimSpace(content),
		MessageID: ts,
		ThreadID:  ts,
	}

	if len(files) > 0 {
		file := files[0]
		msg.FileKey = file.ID
		msg.FileName = file.Name
		msg.FileSize = int64(file.Size)
		msg.Extra = map[string]string{
			"url_private_download": file.URLPrivateDownload,
		}
		if strings.HasPrefix(file.Mimetype, "image/") {
			msg.MessageType = im.MessageTypeImage
		} else {
			msg.MessageType = im.MessageTypeFile
		}
	} else {
		msg.MessageType = im.MessageTypeText
	}

	return msg
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformSlack
}

func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.signingSecret == "" {
		return nil
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	sv, err := slack.NewSecretsVerifier(c.Request.Header, a.signingSecret)
	if err != nil {
		return fmt.Errorf("new secrets verifier: %w", err)
	}
	if _, err := sv.Write(bodyBytes); err != nil {
		return fmt.Errorf("write body to verifier: %w", err)
	}
	if err := sv.Ensure(); err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}

	return nil
}

func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(bodyBytes), slackevents.OptionNoVerifyToken())
	if err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}

	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		var rawEvent struct {
			Event struct {
				Files []slack.File `json:"files"`
			} `json:"event"`
		}
		_ = json.Unmarshal(bodyBytes, &rawEvent)
		files := rawEvent.Event.Files

		innerEvent := eventsAPIEvent.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			threadTs := ev.ThreadTimeStamp
			if threadTs == "" {
				threadTs = ev.TimeStamp
			}
			return parseIncomingMessage(ev.User, ev.Channel, ev.Text, threadTs, im.ChatTypeGroup, files), nil
		case *slackevents.MessageEvent:
			if ev.BotID != "" || (ev.SubType != "" && ev.SubType != "file_share") {
				return nil, nil
			}
			chatType := im.ChatTypeDirect
			if ev.ChannelType == "channel" || ev.ChannelType == "group" {
				chatType = im.ChatTypeGroup
			}
			threadTs := ev.ThreadTimeStamp
			if threadTs == "" {
				threadTs = ev.TimeStamp
			}
			return parseIncomingMessage(ev.User, ev.Channel, ev.Text, threadTs, chatType, files), nil
		}
	}

	return nil, nil
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var body struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return false
	}

	if body.Type == "url_verification" {
		c.JSON(http.StatusOK, gin.H{"challenge": body.Challenge})
		return true
	}

	return false
}

func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.UserID
	}

	options := []slack.MsgOption{slack.MsgOptionText(reply.Content, false)}
	if incoming.MessageID != "" {
		options = append(options, slack.MsgOptionTS(incoming.MessageID))
	}

	_, _, err := a.api.PostMessageContext(ctx, channelID, options...)
	if err != nil {
		return fmt.Errorf("slack post message: %w", err)
	}
	return nil
}

// slackStreamState tracks per-stream accumulated content.
type slackStreamState struct {
	mu      sync.Mutex
	content strings.Builder
	ts      string // The timestamp of the message being updated
	channel string // The channel ID
}

var (
	slackStreamsMu sync.Mutex
	slackStreams   = map[string]*slackStreamState{}
)

func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.UserID
	}

	options := []slack.MsgOption{slack.MsgOptionText("正在思考...", false)}
	if incoming.MessageID != "" {
		options = append(options, slack.MsgOptionTS(incoming.MessageID))
	}

	// Send initial "Thinking..." message
	_, ts, err := a.api.PostMessageContext(ctx, channelID, options...)
	if err != nil {
		return "", fmt.Errorf("slack start stream: %w", err)
	}

	streamID := fmt.Sprintf("%s:%s", channelID, ts)

	slackStreamsMu.Lock()
	slackStreams[streamID] = &slackStreamState{
		ts:      ts,
		channel: channelID,
	}
	slackStreamsMu.Unlock()

	logger.Infof(ctx, "[Slack] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

func (a *Adapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	if content == "" {
		return nil
	}

	slackStreamsMu.Lock()
	state, ok := slackStreams[streamID]
	slackStreamsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	state.content.WriteString(content)
	fullContent := state.content.String()
	state.mu.Unlock()

	// Update the message
	logger.Infof(ctx, "[Slack] Updating stream chunk: stream_id=%s, content=%s", streamID, fullContent)
	_, _, _, err := a.api.UpdateMessageContext(ctx, state.channel, state.ts, slack.MsgOptionText(fullContent, false))
	if err != nil {
		// slack has rate limit, so we just log the error
		// see: https://docs.slack.dev/reference/methods/chat.update/
		logger.Warnf(ctx, "[Slack] Failed to update stream chunk: %v", err)
	}

	return nil
}

func (a *Adapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	slackStreamsMu.Lock()
	state, ok := slackStreams[streamID]
	delete(slackStreams, streamID)
	slackStreamsMu.Unlock()

	if !ok {
		return nil
	}

	state.mu.Lock()
	fullContent := state.content.String()
	state.mu.Unlock()

	_, _, _, err := a.api.UpdateMessageContext(ctx, state.channel, state.ts, slack.MsgOptionText(fullContent, false))
	if err != nil {
		logger.Warnf(ctx, "[Slack] Failed to end stream: %v", err)
	}

	logger.Infof(ctx, "[Slack] Streaming ended: stream_id=%s", streamID)
	return nil
}

func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("file_key is required")
	}

	downloadURL := ""
	if msg.Extra != nil {
		downloadURL = msg.Extra["url_private_download"]
	}

	if downloadURL == "" {
		file, _, _, err := a.api.GetFileInfoContext(ctx, msg.FileKey, 0, 0)
		if err != nil {
			return nil, "", fmt.Errorf("get file info: %w", err)
		}
		downloadURL = file.URLPrivateDownload
	}

	if downloadURL == "" {
		return nil, "", fmt.Errorf("no download URL available for file %s", msg.FileKey)
	}

	pr, pw := io.Pipe()
	go func() {
		err := a.api.GetFileContext(ctx, downloadURL, pw)
		pw.CloseWithError(err)
	}()

	return pr, msg.FileName, nil
}
