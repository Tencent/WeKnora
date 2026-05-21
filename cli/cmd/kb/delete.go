package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// kbDeleteFields enumerates the fields surfaced for `--format json` discovery
// on `kb delete`. The result payload is a small {id, deleted} object.
var kbDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	Yes bool // sourced from the global -y/--yes persistent flag (see cli/cmd/root.go addGlobalFlags)
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
		Use:   "delete <kb-id>",
		Short: "Delete a knowledge base",
		Long: `Permanently deletes a knowledge base and all its contents.

Prompts for confirmation by default when stdout is a TTY and JSON output is not set.
Pass -y/--yes (global flag) to skip the prompt (required in agent / CI / piped contexts).

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead — the exit-10 protocol exists
exactly to guard against unintended deletes.`,
		Example: `  weknora kb delete kb_abc                      # interactive confirm
  weknora kb delete kb_abc -y                   # no prompt
  weknora kb delete kb_abc -y --format json     # bare {id, deleted:true} JSON`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, fopts, cli, f.Prompter(), args[0])
		},
	}
	cmdutil.AddFormatFlag(cmd, kbDeleteFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "permanently delete a knowledge base and all its contents",
		RequiredFlags: []string{"<kb-id> (positional)"},
		Examples: []string{
			"weknora kb delete kb_abc -y",
			"weknora kb delete kb_abc -y --format json",
		},
		Warnings: []string{
			"kb delete is irreversible; whole KB + docs + chunks gone. Never auto-add -y; surface the exit-10 prompt to the user and only retry after explicit approval.",
		},
	})
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "knowledge base", id, "kb.delete", "weknora kb delete "+id+" -y"); err != nil {
		return err
	}

	if err := svc.DeleteKnowledgeBase(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete knowledge base %s", id)
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: id, Deleted: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted knowledge base %s\n", id)
	return nil
}
