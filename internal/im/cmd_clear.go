package im

import "context"

// ClearCommand implements /clear.
// It soft-deletes the current ChannelSession and clears the LLM context so
// the next message starts a completely fresh conversation.
//
// It is the only session-reset entry point on every channel: a LINE channel
// does not get a second custom keyword.
type ClearCommand struct{}

func newClearCommand() *ClearCommand { return &ClearCommand{} }

func (c *ClearCommand) Name() string { return "clear" }
func (c *ClearCommand) Description() string {
	return "清空对话记忆，下次消息将开始全新会话"
}

// Execute clears the session and confirms in the channel's reply language.
// The language comes from the IM identity context (channel locale, else the
// deployment default), which is the same value that drives the model's answer
// language, so the confirmation does not switch language mid-conversation.
func (c *ClearCommand) Execute(ctx context.Context, _ *CommandContext, _ []string) (*CommandResult, error) {
	return &CommandResult{
		Content: localizeCopy(ctx,
			"✅ 对话已清空，下次消息将开始全新会话。",
			"✅ 對話已清空，下一則訊息會開始全新會話。",
		),
		Action: ActionClear,
	}, nil
}
