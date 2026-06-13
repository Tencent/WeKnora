package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

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

const (
	extraKeyThreadRoot = "thread_root_id"
	extraKeyChannelID  = "channel_id"
)

// Adapter implements im integration for Mattermost (outgoing webhook inbound + REST outbound).
type Adapter struct {
	client        *Client
	outgoingToken string
	botUserID     string
	// postReplyToMain: when true, bot replies are new top-level channel posts (visible in main timeline).
	// When false (default), replies use root_id tied to the trigger post so they appear in Mattermost threads.
	postReplyToMain bool
}

// NewAdapter creates a Mattermost adapter.
func NewAdapter(client *Client, outgoingToken, botUserID string, postReplyToMain bool) *Adapter {
	return &Adapter{
		client:          client,
		outgoingToken:   strings.TrimSpace(outgoingToken),
		botUserID:       strings.TrimSpace(botUserID),
		postReplyToMain: postReplyToMain,
	}
}

// outgoingPayload matches Mattermost outgoing webhook parameters (JSON or form).
type outgoingPayload struct {
	Token      string          `json:"token"`
	UserID     string          `json:"user_id"`
	UserName   string          `json:"user_name"`
	ChannelID  string          `json:"channel_id"`
	PostID     string          `json:"post_id"`
	Text       string          `json:"text"`
	RootID     string          `json:"root_id"`
	FileIDsRaw json.RawMessage `json:"file_ids"`
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformMattermost
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

func (a *Adapter) VerifyCallback(c *gin.Context) error {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	payload, err := parseOutgoingBody(c.Request.Header.Get("Content-Type"), bodyBytes)
	if err != nil {
		return fmt.Errorf("parse outgoing payload: %w", err)
	}

	if a.outgoingToken != "" && payload.Token != a.outgoingToken {
		return fmt.Errorf("invalid outgoing webhook token")
	}

	return nil
}

func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	payload, err := parseOutgoingBody(c.Request.Header.Get("Content-Type"), bodyBytes)
	if err != nil {
		return nil, err
	}

	if a.botUserID != "" && payload.UserID == a.botUserID {
		logger.Infof(c.Request.Context(), "[Mattermost] Skip callback: user_id matches bot_user_id (avoid self-reply loop)")
		return nil, nil
	}

	if strings.TrimSpace(payload.Text) == "" && len(parseFileIDs(payload.FileIDsRaw)) == 0 {
		logger.Infof(c.Request.Context(), "[Mattermost] Skip callback: empty text and no file_ids")
		return nil, nil
	}

	var threadRoot string
	if a.postReplyToMain {
		threadRoot = ""
	} else {
		threadRoot = payload.RootID
		if threadRoot == "" {
			// Outgoing webhooks may omit root_id for thread replies.
			// Fetch the post to resolve the actual thread root.
			if actualRootID, err := a.client.GetPost(c.Request.Context(), payload.PostID); err == nil && actualRootID != "" {
				threadRoot = actualRootID
			} else {
				// Top-level message: use its own PostID as thread root.
				threadRoot = payload.PostID
			}
		}
	}

	msg := &im.IncomingMessage{
		Platform:  im.PlatformMattermost,
		UserID:    payload.UserID,
		UserName:  payload.UserName,
		ChatID:    payload.ChannelID,
		ChatType:  im.ChatTypeGroup,
		Content:   strings.TrimSpace(payload.Text),
		MessageID: payload.PostID,
		ThreadID:  threadRoot,
		Extra: map[string]string{
			extraKeyThreadRoot: threadRoot,
			extraKeyChannelID:  payload.ChannelID,
		},
	}

	fileIDs := parseFileIDs(payload.FileIDsRaw)
	if len(fileIDs) > 0 {
		msg.MessageType = im.MessageTypeFile
		msg.FileKey = fileIDs[0]
		if len(fileIDs) > 1 {
			msg.Extra["file_ids"] = strings.Join(fileIDs, ",")
		}
	} else {
		msg.MessageType = im.MessageTypeText
	}

	return msg, nil
}

func parseOutgoingBody(contentType string, body []byte) (*outgoingPayload, error) {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))

	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		var p outgoingPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		return &p, nil

	case ct == "application/x-www-form-urlencoded" || ct == "":
		// Parse as form (Mattermost default for some configs).
		values, err := parseFormBody(body)
		if err != nil {
			return nil, err
		}
		p := &outgoingPayload{
			Token:     values.Get("token"),
			UserID:    values.Get("user_id"),
			UserName:  values.Get("user_name"),
			ChannelID: values.Get("channel_id"),
			PostID:    values.Get("post_id"),
			Text:      values.Get("text"),
			RootID:    values.Get("root_id"),
		}
		if f := values.Get("file_ids"); f != "" {
			p.FileIDsRaw = json.RawMessage(jsonArrayFromCSV(f))
		}
		return p, nil

	default:
		// Try JSON fallback for unknown types.
		var p outgoingPayload
		if err := json.Unmarshal(body, &p); err == nil && (p.Token != "" || p.ChannelID != "") {
			return &p, nil
		}
		return nil, fmt.Errorf("unsupported content-type: %s", contentType)
	}
}

func parseFileIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return splitFileIDs(s)
	}
	return nil
}

func splitFileIDs(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.Extra[extraKeyChannelID]
	}
	if channelID == "" {
		return fmt.Errorf("missing channel_id")
	}

	threadRoot := ""
	if incoming.Extra != nil {
		threadRoot = incoming.Extra[extraKeyThreadRoot]
	}

	_, err := a.client.CreatePost(ctx, channelID, threadRoot, reply.Content)
	return err
}

type mmStreamState struct {
	mu      sync.Mutex
	content strings.Builder
	postID  string
	channel string
}

var (
	mmStreamsMu sync.Mutex
	mmStreams   = map[string]*mmStreamState{}
)

func (a *Adapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	channelID := incoming.ChatID
	if channelID == "" {
		channelID = incoming.Extra[extraKeyChannelID]
	}
	threadRoot := ""
	if incoming.Extra != nil {
		threadRoot = incoming.Extra[extraKeyThreadRoot]
	}

	postID, err := a.client.CreatePost(ctx, channelID, threadRoot, "正在思考...")
	if err != nil {
		return "", err
	}

	streamID := channelID + ":" + postID
	mmStreamsMu.Lock()
	mmStreams[streamID] = &mmStreamState{postID: postID, channel: channelID}
	mmStreamsMu.Unlock()

	logger.Infof(ctx, "[Mattermost] Streaming started: stream_id=%s", streamID)
	return streamID, nil
}

func (a *Adapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	if content == "" {
		return nil
	}

	mmStreamsMu.Lock()
	state, ok := mmStreams[streamID]
	mmStreamsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream ID: %s", streamID)
	}

	state.mu.Lock()
	state.content.WriteString(content)
	full := state.content.String()
	postID := state.postID
	state.mu.Unlock()

	if err := a.client.PatchPostMessage(ctx, postID, full); err != nil {
		logger.Warnf(ctx, "[Mattermost] Patch post failed: %v", err)
	}
	return nil
}

func (a *Adapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	mmStreamsMu.Lock()
	state, ok := mmStreams[streamID]
	delete(mmStreams, streamID)
	mmStreamsMu.Unlock()

	if !ok {
		return nil
	}

	state.mu.Lock()
	full := state.content.String()
	postID := state.postID
	state.mu.Unlock()

	if err := a.client.PatchPostMessage(ctx, postID, full); err != nil {
		logger.Warnf(ctx, "[Mattermost] EndStream patch failed: %v", err)
	}
	logger.Infof(ctx, "[Mattermost] Streaming ended: post_id=%s", postID)
	return nil
}

func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("file_key is required")
	}

	info, err := a.client.GetFileInfo(ctx, msg.FileKey)
	if err != nil {
		return nil, "", fmt.Errorf("file info: %w", err)
	}

	name := info.Name
	if name == "" {
		name = msg.FileName
	}
	if name == "" {
		name = msg.FileKey
	}

	rc, err := a.client.GetFileReader(ctx, msg.FileKey)
	if err != nil {
		return nil, "", err
	}

	return rc, name, nil
}
