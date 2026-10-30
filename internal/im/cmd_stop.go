package im

import "context"

// StopCommand implements /stop.
// It cancels the in-flight QA request for the current user+chat, allowing the
// user to abort a long-running ReAct reasoning chain without waiting for it to
// complete. If no request is in progress the command simply acknowledges.
type StopCommand struct{}

func newStopCommand() *StopCommand { return &StopCommand{} }

func (c *StopCommand) Name() string        { return "stop" }
func (c *StopCommand) Description() string { return "中止當前正在進行的回答" }

func (c *StopCommand) Execute(_ context.Context, _ *CommandContext, _ []string) (*CommandResult, error) {
	return &CommandResult{
		Content: "✅ 已請求中止當前回答。",
		Action:  ActionStop,
	}, nil
}
