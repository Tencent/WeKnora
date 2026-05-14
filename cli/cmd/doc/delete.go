package doc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// docDeleteFields enumerates the fields surfaced for `--json` discovery on
// `doc delete`. The result payload is a small {id, deleted} object.
var docDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	Yes bool // sourced from the global -y/--yes persistent flag (see cli/cmd/root.go)
}

// DeleteService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type DeleteService interface {
	DeleteKnowledge(ctx context.Context, id string) error
}

// deleteResult is the typed payload emitted under data on success.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// NewCmdDelete builds `weknora doc delete`. Confirmation routed through
// the global -y/--yes persistent flag.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a document from a knowledge base",
		Long: `Permanently deletes one document. Prompts for confirmation by default
when stdout is a TTY and --json is not set; pass -y/--yes (global flag) to skip
the prompt (required in agent / CI / piped contexts).

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10 and
returns an envelope describing the missing confirmation. NEVER auto-pass -y
without the user's explicit go-ahead.`,
		Example: `  weknora doc delete doc_abc           # interactive confirm
  weknora doc delete doc_abc -y        # no prompt
  weknora doc delete doc_abc -y --json # envelope output`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			opts.Yes, _ = c.Flags().GetBool("yes")
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, jopts, cli, f.Prompter(), args[0])
		},
	}
	cmdutil.AddJSONFlags(cmd, docDeleteFields)
	aiclient.SetAgentHelp(cmd, "Destructively deletes one document by id. ALWAYS pass -y/--yes in agent mode (no TTY ⇒ confirm prompt fails). Returns data: {id, deleted:true}.")
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, jopts *cmdutil.JSONOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, jopts.Enabled(), "document", id); err != nil {
		return err
	}

	if err := svc.DeleteKnowledge(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete document %s", id)
	}

	if jopts.Enabled() {
		return format.WriteJSONFiltered(iostreams.IO.Out, deleteResult{ID: id, Deleted: true}, jopts.Fields, jopts.JQ)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted document %s\n", id)
	return nil
}
