package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// DeleteOptions captures `weknora kb delete` flags.
type DeleteOptions struct {
	Force   bool
	JSONOut bool
}

// DeleteService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type DeleteService interface {
	DeleteKnowledgeBase(ctx context.Context, id string) error
}

// deleteResult is the typed payload emitted under data on success.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// NewCmdDelete builds `weknora kb delete`.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a knowledge base",
		Long: `Permanently deletes a knowledge base and all its contents.

Prompts for confirmation by default when stdout is a TTY and --json is not set.
Pass --force to skip the prompt (required in agent / CI / piped contexts).`,
		Example: `  weknora kb delete kb_abc                # interactive confirm
  weknora kb delete kb_abc --force        # no prompt
  weknora kb delete kb_abc --force --json # envelope output`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, cli, f.Prompter(), args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Destructively deletes a knowledge base by id. ALWAYS pass --force in agent mode (no TTY ⇒ confirm prompt fails). Returns data: {id, deleted:true}.")
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Force, opts.JSONOut, "knowledge base", id); err != nil {
		return err
	}

	if err := svc.DeleteKnowledgeBase(ctx, id); err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "delete knowledge base %s", id)
	}

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(deleteResult{ID: id, Deleted: true}, &format.Meta{
			KBID: id,
		}))
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted knowledge base %s\n", id)
	return nil
}
