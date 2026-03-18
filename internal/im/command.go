package im

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// CommandAction represents a service-level side effect that a command requests.
// Using an enum keeps commands free of service/DB dependencies—they declare intent,
// the Service executes it.
type CommandAction int

const (
	// ActionNone means no side effect beyond sending the reply.
	ActionNone CommandAction = iota
	// ActionClear soft-deletes the current ChannelSession and clears the LLM
	// context so the next message creates a completely fresh conversation.
	ActionClear
	// ActionStop cancels the in-flight QA request for this user+chat.
	ActionStop
)

// CommandResult is the output produced by a Command.Execute call.
type CommandResult struct {
	// Content is the Markdown reply sent back to the user.
	Content string
	// Action requests a service-level side effect (reset, clear, …).
	Action CommandAction
}

// CommandContext carries all runtime data a command needs during execution.
// Services are NOT here; inject them into command structs at construction time.
type CommandContext struct {
	// Incoming is the raw IM message that triggered the command.
	Incoming *IncomingMessage
	// Session is the IM channel session for this user×chat combination.
	Session *ChannelSession
	// TenantID is the tenant that owns this bot deployment.
	TenantID uint64
	// AgentName is the display name of the bound agent (empty if none).
	AgentName string
	// CustomAgent is the bound agent configuration. Commands that need to
	// inspect agent-level settings (e.g. /search reading KBSelectionMode)
	// can access it directly. May be nil when no agent is bound.
	CustomAgent *types.CustomAgent
	// ChannelOutputMode is the channel-level output mode configured by the admin
	// ("stream" or "full").
	ChannelOutputMode string
}

// Command is the interface every IM slash-command must implement.
//
// Design rules:
//   - Dependencies (DB, services) are injected at construction time.
//   - Validation errors (bad args, entity not found) are returned as a
//     CommandResult with a helpful message, NOT as an error.
//   - error is reserved for infrastructure failures (DB errors, network, …).
type Command interface {
	// Name is the primary token used after "/" (e.g. "kb", "mode").
	Name() string
	// Description is the one-line summary shown in /help output.
	Description() string
	// Execute runs the command and returns a reply to send to the user.
	Execute(ctx context.Context, cmdCtx *CommandContext, args []string) (*CommandResult, error)
}
