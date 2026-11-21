package types

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// IMConversationContextKey carries the trusted conversation through QA work.
const IMConversationContextKey ContextKey = "IMConversation"

// IMConversation identifies the verified messaging conversation, not a model
// argument or an HTTP header. Missing chat IDs can only match an explicit "*".
type IMConversation struct {
	Platform string
	ChatType string
	ChatID   string
}

// WithIMConversation marks a context as originating from an IM conversation.
func WithIMConversation(ctx context.Context, conversation IMConversation) context.Context {
	return context.WithValue(ctx, IMConversationContextKey, conversation)
}

// IMConversationFromContext returns the conversation and whether the request is IM.
func IMConversationFromContext(ctx context.Context) (IMConversation, bool) {
	if ctx == nil {
		return IMConversation{}, false
	}
	conversation, ok := ctx.Value(IMConversationContextKey).(IMConversation)
	return conversation, ok
}

// MCPToolSessionRule permits an exact conversation, or every conversation of
// one platform and chat type when ChatID is "*". Platform/type never default to
// wildcards, so an incomplete rule cannot accidentally broaden access.
type MCPToolSessionRule struct {
	Platform string `json:"platform"`
	ChatType string `json:"chat_type"`
	ChatID   string `json:"chat_id"`
}

// ErrInvalidMCPToolSessionPolicy identifies an invalid allowlist request.
var ErrInvalidMCPToolSessionPolicy = errors.New("invalid MCP tool session policy")

// Validate rejects incomplete identities and implicit or partial wildcards.
func (r MCPToolSessionRule) Validate() error {
	switch r.Platform {
	case "wecom", "feishu", "lark", "telegram", "dingtalk", "mattermost", "wechat", "qqbot", "slack", "yunzhijia":
	default:
		return fmt.Errorf("%w: a supported platform is required", ErrInvalidMCPToolSessionPolicy)
	}
	if r.ChatType != "direct" && r.ChatType != "group" {
		return fmt.Errorf("%w: chat_type must be direct or group", ErrInvalidMCPToolSessionPolicy)
	}
	if r.ChatID == "" || len(r.ChatID) > 512 || strings.TrimSpace(r.ChatID) != r.ChatID ||
		strings.ContainsFunc(r.ChatID, unicode.IsControl) ||
		(r.ChatID != "*" && strings.Contains(r.ChatID, "*")) {
		return fmt.Errorf("%w: chat_id must be an exact ID or *", ErrInvalidMCPToolSessionPolicy)
	}
	return nil
}

// Matches tests a validated rule against the trusted conversation identity.
func (r MCPToolSessionRule) Matches(conversation IMConversation) bool {
	return r.Validate() == nil && r.Platform == conversation.Platform && r.ChatType == conversation.ChatType &&
		(r.ChatID == "*" || r.ChatID == conversation.ChatID)
}

// MCPToolSessionPolicy only narrows IM access. No row means no additional
// restriction; an existing row with no allowed sessions denies all IM callers.
type MCPToolSessionPolicy struct {
	TenantID        uint64               `json:"tenant_id" gorm:"primaryKey;autoIncrement:false"`
	ServiceID       string               `json:"service_id" gorm:"type:varchar(36);primaryKey"`
	ToolName        string               `json:"tool_name" gorm:"type:varchar(512);primaryKey"`
	AllowedSessions []MCPToolSessionRule `json:"allowed_sessions" gorm:"serializer:json;type:jsonb;not null"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// TableName keeps IM restrictions separate from approval and risk policies.
func (MCPToolSessionPolicy) TableName() string { return "mcp_tool_session_policies" }

// Allows applies OR semantics; an empty allowlist permits no IM conversation.
func (p *MCPToolSessionPolicy) Allows(conversation IMConversation) bool {
	for _, rule := range p.AllowedSessions {
		if rule.Matches(conversation) {
			return true
		}
	}
	return false
}
