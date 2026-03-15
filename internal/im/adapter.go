package im

import (
	"context"

	"github.com/gin-gonic/gin"
)

// Platform identifies an IM platform.
type Platform string

const (
	PlatformWeCom  Platform = "wecom"
	PlatformFeishu Platform = "feishu"
)

// IncomingMessage is the unified message parsed from an IM callback.
type IncomingMessage struct {
	// Platform identifies which IM platform the message comes from.
	Platform Platform
	// UserID is the IM-platform user identifier.
	UserID string
	// UserName is the display name of the user (optional).
	UserName string
	// ChatID is the group/channel ID (empty for direct messages).
	ChatID string
	// ChatType distinguishes direct message from group chat.
	ChatType ChatType
	// Content is the text content of the message.
	Content string
	// MessageID is the IM-platform message identifier (for dedup).
	MessageID string
	// Extra holds platform-specific fields (e.g., WeCom stream ID).
	Extra map[string]string
}

// ChatType represents the IM chat type.
type ChatType string

const (
	ChatTypeDirect ChatType = "direct"
	ChatTypeGroup  ChatType = "group"
)

// ReplyMessage is what WeKnora sends back to the IM platform.
type ReplyMessage struct {
	// Content is the text content (Markdown).
	Content string
	// IsStreaming indicates whether this is a streaming chunk.
	IsStreaming bool
	// IsFinal marks the last chunk of a streaming reply.
	IsFinal bool
	// Extra holds platform-specific fields.
	Extra map[string]string
}

// Adapter is the interface every IM platform must implement.
type Adapter interface {
	// Platform returns the platform identifier.
	Platform() Platform

	// VerifyCallback verifies the signature/token of an incoming callback request.
	// Returns nil if verification passes.
	VerifyCallback(c *gin.Context) error

	// ParseCallback parses the raw IM callback request into a unified IncomingMessage.
	// Returns nil message for non-message events (e.g., URL verification).
	ParseCallback(c *gin.Context) (*IncomingMessage, error)

	// SendReply sends a reply back to the IM platform.
	SendReply(ctx context.Context, incoming *IncomingMessage, reply *ReplyMessage) error

	// HandleURLVerification handles the initial URL verification challenge from the IM platform.
	// Returns true if this request is a verification request and has been handled.
	HandleURLVerification(c *gin.Context) bool
}
