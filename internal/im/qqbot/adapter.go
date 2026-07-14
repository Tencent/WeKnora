package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
)

var _ im.Adapter = (*Adapter)(nil)

type Adapter struct {
	client *Client
}

func NewAdapter(client *Client) *Adapter {
	return &Adapter{client: client}
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformQQBot
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

func (a *Adapter) VerifyCallback(c *gin.Context) error {
	return nil
}

func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var payload gatewayPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}
	return parseGatewayPayload(&payload)
}

func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	content := strings.TrimSpace(im.FormatIMDisplayContent(reply.Content, im.StreamDisplayFinal))
	if content == "" {
		return nil
	}
	msgID := ""
	if incoming.Extra != nil {
		msgID = incoming.Extra[extraKeyMessageID]
	}
	if incoming.ChatType == im.ChatTypeGroup {
		return a.client.SendGroupMessage(ctx, incoming.ChatID, content, msgID)
	}
	return a.client.SendC2CMessage(ctx, incoming.UserID, content, msgID)
}

func parseGatewayPayload(payload *gatewayPayload) (*im.IncomingMessage, error) {
	if payload == nil || payload.Op != opDispatch {
		return nil, nil
	}
	var event messageEvent
	if err := json.Unmarshal(payload.D, &event); err != nil {
		return nil, err
	}
	switch payload.T {
	case eventC2CMessageCreate:
		return parseC2CMessage(&event), nil
	case eventGroupAtMessageCreate:
		return parseGroupMessage(&event), nil
	default:
		return nil, nil
	}
}

func parseC2CMessage(event *messageEvent) *im.IncomingMessage {
	userID := firstNonEmpty(event.Author.UserOpenID, event.Author.ID)
	return &im.IncomingMessage{
		Platform:    im.PlatformQQBot,
		MessageType: im.MessageTypeText,
		UserID:      userID,
		UserName:    event.Author.Username,
		ChatID:      "",
		ChatType:    im.ChatTypeDirect,
		Content:     strings.TrimSpace(event.Content),
		MessageID:   event.ID,
		Extra: map[string]string{
			extraKeyMessageID: event.ID,
			extraKeyChatKind:  "c2c",
		},
	}
}

func parseGroupMessage(event *messageEvent) *im.IncomingMessage {
	return &im.IncomingMessage{
		Platform:    im.PlatformQQBot,
		MessageType: im.MessageTypeText,
		UserID:      firstNonEmpty(event.Author.MemberOpenID, event.Author.UserOpenID, event.Author.ID),
		UserName:    event.Author.Username,
		ChatID:      event.GroupOpenID,
		ChatType:    im.ChatTypeGroup,
		Content:     strings.TrimSpace(event.Content),
		MessageID:   event.ID,
		Extra: map[string]string{
			extraKeyMessageID: event.ID,
			extraKeyChatKind:  "group",
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
