package im

import "context"

// ResetCommand clears the current session context and replies with a
// confirmation. It backs per-channel custom reset keywords (LINE
// reset_keyword such as "/reset", "/new", "/restart").
//
// It reuses the same ActionClear side effect as ClearCommand — the conversation
// history is forgotten — but emits a channel-friendly confirmation instead of
// the built-in "/clear" text. It is not registered in CommandRegistry: the
// reset keyword is configured per channel, so the service instantiates it on
// demand (see service.go) and dispatches through handleCommand.
type ResetCommand struct{}

var _ Command = (*ResetCommand)(nil)

// NewResetCommand builds the session-reset command.
func NewResetCommand() Command { return &ResetCommand{} }

// Name identifies the command in help output and error logs.
func (*ResetCommand) Name() string { return "reset" }

// Description is a one-line summary shown in /help output.
func (*ResetCommand) Description() string {
	return "Reset this conversation's memory and start fresh"
}

// Execute clears the session context and returns the confirmation reply.
func (*ResetCommand) Execute(context.Context, *CommandContext, []string) (*CommandResult, error) {
	return &CommandResult{
		Content: "會話已重置 / Session cleared",
		Action:  ActionClear,
	}, nil
}
