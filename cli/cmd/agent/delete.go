package agentcmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// agentDeleteFields enumerates the JSON discovery fields for `agent delete`.
// Result payload is a tiny {id, deleted} object — mirrors `kb delete`.
var agentDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	AgentID string
	Yes     bool // sourced from the global -y/--yes persistent flag
}

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteAgent(ctx context.Context, id string) error
}

// deleteResult is the typed payload emitted on success in JSON mode.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// Delete is NOT idempotent on a missing id — it surfaces resource.not_found
// rather than silently exiting 0. Idempotent-already-true semantics are
// reserved for unlink-style local cleanups, not server-side resource removal.
const agentDeleteLong = `Permanently delete a custom agent.

Prompts for confirmation by default when stdout is a TTY and --format json is
not set. Pass -y/--yes (the global flag) to skip the prompt (required in
agent / CI / piped contexts).

Typed exit codes:
  resource.not_found            no agent with the given id (exit 4)
  auth.forbidden                caller lacks delete permission on the agent (exit 3)
  input.confirmation_required   destructive op without -y on a TTY (exit 10)

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead — the exit-10 protocol exists
exactly to guard against unintended deletes.`

const agentDeleteExample = `  weknora agent delete ag_abc           # interactive confirm
  weknora agent delete ag_abc -y        # no prompt
  weknora agent delete ag_abc -y --format json # bare {id, deleted:true} JSON`

// NewCmdDelete builds `weknora agent delete <agent-id>`.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:     "delete <agent-id>",
		Short:   "Delete a custom agent",
		Long:    agentDeleteLong,
		Example: agentDeleteExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(cmd)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.AgentID = args[0]
			opts.Yes, _ = cmd.Flags().GetBool("yes")
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(cmd.Context(), opts, fopts, cli, f.Prompter())
		},
	}
	cmdutil.AddFormatFlag(cmd, agentDeleteFields...)
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "agent", opts.AgentID); err != nil {
		return err
	}
	if err := svc.DeleteAgent(ctx, opts.AgentID); err != nil {
		return cmdutil.WrapHTTP(err, "delete agent %s", opts.AgentID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: opts.AgentID, Deleted: true})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted agent %s\n", opts.AgentID)
	return nil
}
