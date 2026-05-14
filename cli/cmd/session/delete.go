package sessioncmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// sessionDeleteFields enumerates the fields surfaced for `--json` discovery on
// `session delete`. Tracks the small result struct.
var sessionDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	Yes bool // sourced from the global -y/--yes persistent flag
}

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteSession(ctx context.Context, id string) error
}

// deleteResult is the typed payload emitted under data on success.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// NewCmdDelete builds `weknora session delete`. Destructive write gated
// by -y/--yes (exit-10 protocol in scripted / --json invocations).
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a chat session",
		Long: `Permanently delete a chat session and its messages.

Prompts for confirmation by default when stdout is a TTY and --json is not set.
Pass -y/--yes (global flag) to skip the prompt (required in agent / CI / piped contexts).

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead.`,
		Example: `  weknora session delete s_abc          # interactive confirm
  weknora session delete s_abc -y       # no prompt
  weknora session delete s_abc -y --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, jopts, cli, f.Prompter(), args[0])
		},
	}
	cmdutil.AddJSONFlags(cmd, sessionDeleteFields)
	aiclient.SetAgentHelp(cmd, "Destructively deletes a chat session by id. ALWAYS pass -y/--yes in agent mode (no TTY ⇒ confirm prompt fails). Returns data: {id, deleted:true}.")
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, jopts *cmdutil.JSONOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, jopts.Enabled(), "session", id); err != nil {
		return err
	}

	if err := svc.DeleteSession(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete session %s", id)
	}

	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, deleteResult{ID: id, Deleted: true})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted session %s\n", id)
	return nil
}
