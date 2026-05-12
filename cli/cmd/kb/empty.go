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
	sdk "github.com/Tencent/WeKnora/client"
)

type EmptyOptions struct {
	Yes     bool
	JSONOut bool
	DryRun  bool
}

type EmptyService interface {
	ClearKnowledgeBaseContents(ctx context.Context, id string) (*sdk.ClearKnowledgeBaseContentsResponse, error)
}

// emptyResult is the typed payload emitted under data on success.
type emptyResult struct {
	ID           string `json:"id"`
	DeletedCount int    `json:"deleted_count"`
}

// NewCmdEmpty builds `weknora kb empty <id>`. Wipes every document inside
// the knowledge base; the KB itself is preserved. The server runs the
// delete asynchronously and reports the count of documents that were
// enqueued for removal.
func NewCmdEmpty(f *cmdutil.Factory) *cobra.Command {
	opts := &EmptyOptions{}
	cmd := &cobra.Command{
		Use:   "empty <id>",
		Short: "Delete every document in a knowledge base (preserves the KB)",
		Long: `Removes all documents and chunks from a knowledge base while keeping the
KB record (its name, description, and config) intact. The delete is async;
the server reports the count of items enqueued for removal.

Prompts for confirmation by default; pass -y/--yes to skip in agent / CI /
piped contexts. Without -y the CLI exits 10 in non-interactive mode.`,
		Example: `  weknora kb empty kb_abc           # interactive confirm
  weknora kb empty kb_abc -y --json # agent-friendly`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			opts.DryRun = cmdutil.IsDryRun(c)
			if opts.DryRun {
				return runEmpty(c.Context(), opts, nil, f.Prompter(), args[0])
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runEmpty(c.Context(), opts, cli, f.Prompter(), args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Destructively empties a knowledge base (deletes all documents, preserves the KB record + config). ALWAYS pass -y/--yes in agent mode (non-TTY ⇒ confirm fails). Returns data: {id, deleted_count}. Async — items may still be processing server-side after the call returns.")
	return cmd
}

func runEmpty(ctx context.Context, opts *EmptyOptions, svc EmptyService, p prompt.Prompter, id string) error {
	risk := &format.Risk{Level: format.RiskHighRiskWrite, Action: fmt.Sprintf("empty knowledge base %s", id)}

	if opts.DryRun {
		return cmdutil.EmitDryRun(opts.JSONOut, emptyResult{ID: id}, &format.Meta{KBID: id}, risk)
	}

	if err := cmdutil.ConfirmDestructive(p, opts.Yes, opts.JSONOut, "all contents of knowledge base", id); err != nil {
		return err
	}

	resp, err := svc.ClearKnowledgeBaseContents(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "empty knowledge base %s", id)
	}
	deleted := 0
	if resp != nil {
		deleted = resp.DeletedCount
	}

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.SuccessWithRisk(
			emptyResult{ID: id, DeletedCount: deleted}, &format.Meta{KBID: id}, risk,
		))
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Emptied knowledge base %s (%d document(s) cleared)\n", id, deleted)
	return nil
}
