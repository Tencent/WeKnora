package im

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// HelpCommand implements /help [command].
type HelpCommand struct {
	registry *CommandRegistry
}

func newHelpCommand(registry *CommandRegistry) *HelpCommand {
	return &HelpCommand{registry: registry}
}

func (c *HelpCommand) Name() string { return "help" }
func (c *HelpCommand) Description() string {
	return "显示可用指令列表，或查看某个指令的详细用法"
}

func (c *HelpCommand) Execute(_ context.Context, _ *CommandContext, args []string) (*CommandResult, error) {
	// /help <command> — show detailed usage for a specific command
	if len(args) > 0 {
		name := strings.ToLower(args[0])
		cmd, _, ok := c.registry.Parse("/" + name)
		if !ok {
			return &CommandResult{
				Content: fmt.Sprintf("未知指令 `%s`，发送 `/help` 查看所有可用指令。", args[0]),
			}, nil
		}
		return &CommandResult{
			Content: fmt.Sprintf("**/%s** — %s", cmd.Name(), cmd.Description()),
		}, nil
	}

	// /help — list all commands sorted by name
	cmds := c.registry.All()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name() < cmds[j].Name() })

	var sb strings.Builder
	sb.WriteString("**可用指令**\n\n")
	for _, cmd := range cmds {
		sb.WriteString(fmt.Sprintf("· `/%s` — %s\n", cmd.Name(), cmd.Description()))
	}
	sb.WriteString("\n发送 `/help <指令名>` 查看详细用法")
	return &CommandResult{Content: sb.String()}, nil
}
