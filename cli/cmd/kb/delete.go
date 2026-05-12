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

type DeleteOptions struct {
	Yes     bool // sourced from the global -y/--yes persistent flag (see cli/cmd/root.go addGlobalFlags)
	JSONOut bool
	DryRun  bool
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

// NewCmdDelete builds `weknora kb delete`. The global -y/--yes persistent
// flag is the single skip-prompt switch for the destructive-write
// confirmation pattern.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a knowledge base",
		Long: `Permanently deletes a knowledge base and all its contents.

Prompts for confirmation by default when stdout is a TTY and --json is not set.
Pass -y/--yes (global flag) to skip the prompt (required in agent / CI / piped contexts).

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10 and
returns an envelope describing the missing confirmation. NEVER auto-pass -y
without the user's explicit go-ahead — the exit-10 protocol exists exactly to
guard against unintended deletes.`,
		Example: `  weknora kb delete kb_abc           # interactive confirm
  weknora kb delete kb_abc -y        # no prompt
  weknora kb delete kb_abc -y --json # envelope output`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			opts.DryRun = cmdutil.IsDryRun(c)
			if opts.DryRun {
				return runDelete(c.Context(), opts, nil, f.Prompter(), args[0])
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, cli, f.Prompter(), args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Destructively deletes a knowledge base by id. ALWAYS pass -y/--yes in agent mode (no TTY ⇒ confirm prompt fails). Returns data: {id, deleted:true}.")
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if opts.DryRun {
		return cmdutil.EmitDryRun(opts.JSONOut,
			deleteResult{ID: id, Deleted: false}, &format.Meta{KBID: id},
			&format.Risk{Level: format.RiskHighRiskWrite, Action: fmt.Sprintf("delete knowledge base %s", id)})
	}

	if err := cmdutil.ConfirmDestructive(p, opts.Yes, opts.JSONOut, "knowledge base", id); err != nil {
		return err
	}

	if err := svc.DeleteKnowledgeBase(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete knowledge base %s", id)
	}

	if opts.JSONOut {
		risk := &format.Risk{Level: format.RiskHighRiskWrite, Action: fmt.Sprintf("deleted knowledge base %s", id)}
		return format.WriteEnvelope(iostreams.IO.Out, format.SuccessWithRisk(deleteResult{ID: id, Deleted: true}, &format.Meta{KBID: id}, risk))
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted knowledge base %s\n", id)
	return nil
}
