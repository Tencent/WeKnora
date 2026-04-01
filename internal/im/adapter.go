package im

import (
	"context"
	"io"

	"github.com/gin-gonic/gin"
)

// Platform identifies an IM platform.
type Platform string

const (
	PlatformWeCom      Platform = "wecom"
	PlatformFeishu     Platform = "feishu"
	PlatformSlack      Platform = "slack"
	PlatformTelegram   Platform = "telegram"
	PlatformDingtalk   Platform = "dingtalk"
	PlatformMattermost Platform = "mattermost"
)

// SessionMode determines how IM sessions are resolved.
type SessionMode string

const (
	// SessionModeUser resolves sessions by (platform, user_id, chat_id, tenant_id).
	SessionModeUser SessionMode = "user"
	// SessionModeThread resolves sessions by (platform, thread_id, chat_id, tenant_id).
	SessionModeThread SessionMode = "thread"
)

// MessageType identifies the kind of IM message.
type MessageType string

const (
	MessageTypeText  MessageType = "text"
	MessageTypeFile  MessageType = "file"
	MessageTypeImage MessageType = "image"
)

// IncomingMessage is the unified message parsed from an IM callback.
type IncomingMessage struct {
	// Platform identifies which IM platform the message comes from.
	Platform Platform
	// MessageType is "text" (default) or "file".
	MessageType MessageType
	// UserID is the IM-platform user identifier.
	UserID string
	// UserName is the display name of the user (optional).
	UserName string
	// ChatID is the group/channel ID (empty for direct messages).
	ChatID string
	// ChatType distinguishes direct message from group chat.
	ChatType ChatType
	// Content is the text content of the message (empty for file messages).
	Content string
	// MessageID is the IM-platform message identifier (for dedup).
	MessageID string
	// FileKey is the platform file identifier (for file messages).
	FileKey string
	// FileName is the original file name (for file messages).
	FileName string
	// FileSize is the file size in bytes (for file messages, optional).
	FileSize int64
	// ThreadID is the platform-specific thread identifier.
	// - Slack: thread_ts (top-level message uses its own timestamp)
	// - Mattermost: root_id, or post_id if top-level
	// - Feishu: root_id, or message_id if top-level
	// - Telegram: message_thread_id (Forum Topics only)
	// Empty for platforms without thread support (WeCom, DingTalk).
	// In thread mode, top-level messages use their own ID as ThreadID,
	// effectively creating a new session per top-level message.
	ThreadID string
	// Quote is the quoted/replied message, if any.
	// Populated by adapters on platforms that support quote-reply.
	Quote *QuotedMessage
	// Extra holds platform-specific fields (e.g., WeCom stream ID).
	Extra map[string]string
}

// QuotedMessage holds the content and metadata of a quoted/replied message.
// Populated by platform adapters that support quote-reply (e.g. WeCom long-connection).
type QuotedMessage struct {
	// MessageID is the platform message ID of the quoted message.
	MessageID string
	// Content is the text content. Empty for non-text message types.
	Content string
	// SenderID is the platform user ID of the quoted message's author.
	SenderID string
	// IsBotMessage indicates whether the quoted message was from the bot.
	IsBotMessage bool
	// NonTextType records the original message type when the quoted message
	// has no extractable text (e.g. "image", "file", "video").
	// Empty when Content is populated. Used to generate LLM instructions
	// instead of content placeholders that cause hallucination.
	NonTextType string
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

// StreamSender is an optional interface that adapters can implement to support streaming replies.
// When an adapter implements StreamSender, the IM service will push answer chunks in real-time
// instead of waiting for the full answer.
type StreamSender interface {
	// StartStream initializes a streaming reply session (e.g., creates a streaming card).
	// Returns a platform-specific stream ID for subsequent chunk/end calls.
	StartStream(ctx context.Context, incoming *IncomingMessage) (string, error)

	// SendStreamChunk appends a content chunk to an ongoing stream.
	SendStreamChunk(ctx context.Context, incoming *IncomingMessage, streamID string, content string) error

	// EndStream finalizes a streaming reply.
	EndStream(ctx context.Context, incoming *IncomingMessage, streamID string) error
}

// FileDownloader is an optional interface that adapters can implement to support
// downloading file attachments from the IM platform. When the adapter implements
// this interface and the IM channel has a knowledge_base_id configured, file
// messages will be downloaded and saved to the specified knowledge base.
type FileDownloader interface {
	// DownloadFile downloads a file resource from the IM platform.
	// Returns the file content reader, the resolved file name, and any error.
	DownloadFile(ctx context.Context, msg *IncomingMessage) (io.ReadCloser, string, error)
}
