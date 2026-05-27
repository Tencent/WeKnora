package doc

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	sdk "github.com/Tencent/WeKnora/client"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// docDeleteFields enumerates the fields surfaced for `--format json` discovery
// on `doc delete`. The result payload is a small {id, deleted} object.
var docDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	Yes    bool   // sourced from the global -y/--yes persistent flag (see cli/cmd/root.go)
	All    bool   // delete all docs in --kb
	KB     string // required when --all
	DryRun bool
}

// DeleteService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type DeleteService interface {
	DeleteKnowledge(ctx context.Context, id string) error
}

// AllService is the narrow SDK surface for --all mode.
// *sdk.Client satisfies it.
type AllService interface {
	ClearKnowledgeBaseContents(ctx context.Context, kbID string) (*sdk.ClearKnowledgeBaseContentsResponse, error)
}

// deleteResult is the typed payload emitted under data on success (single-id).
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// NewCmdDelete builds `weknora doc delete`. Single-id keeps the simpler
// code path (one confirm prompt, exit 0/1); multi-id uses keep-going
// semantics (one -y confirms all, failures collected, exit 1 if any fail);
// --all --kb=<id> atomically clears every document in a knowledge base.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <doc-id> [<doc-id>...] | --all --kb=<kb-id>",
		Short: "Delete one or more documents from a knowledge base",
		Long: `Permanently deletes one or more documents. Prompts for confirmation by
default when stdout is a TTY and JSON output is not set; pass -y/--yes
(global flag) to skip the prompt (required in agent / CI / piped contexts).

Single-id: one confirm prompt, exit 0/1.
Multi-id:
  • Default keep-going: failed deletes do NOT stop the run; failures collected.
  • One -y/--yes confirms all documents.
  • TTY prompt shows total: "Delete N document(s)? This cannot be undone."
  • Exit 0 if all succeed; exit 1 if any failed.

All-in-KB (--all --kb=<kb-id>):
  • Atomically clears every document in the named knowledge base.
  • The KB record itself (name, config) is preserved.
  • Mutually exclusive with positional doc ids.
  • Exit 0 on success; exit 10 without -y in non-interactive/JSON mode.

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead.`,
		Example: `  weknora doc delete doc_abc                        # interactive confirm
  weknora doc delete doc_abc -y                     # no prompt
  weknora doc delete doc_abc -y --format json       # bare {id, deleted:true} JSON
  weknora doc delete doc_a doc_b doc_c -y           # delete 3, keep-going
  weknora doc delete doc_a doc_b --format json      # multi-id JSON output
  weknora doc delete --all --kb=kb_x -y             # clear all docs in kb_x
  weknora doc delete --all --kb=kb_x -y --format json # agent-friendly`,
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.Yes, _ = c.Flags().GetBool("yes")
			// Structural validation (pure-local) must run before the dry-run
			// gate so --dry-run rejects the same invalid invocations the live
			// path rejects. Same typed errors are kept below the gate for
			// safety (in case of future callers that bypass RunE).
			if opts.All {
				if opts.KB == "" {
					return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--all requires --kb=<id>").
						WithHint("specify --kb=<kb-id> to scope the delete-all operation").
						WithRetryCommand("weknora doc delete --all --kb=<kb-id> -y")
				}
				if len(args) > 0 {
					return cmdutil.NewFlagError(fmt.Errorf("--all is exclusive with positional doc ids"))
				}
			} else if len(args) == 0 {
				return cmdutil.NewFlagError(fmt.Errorf("doc id(s) required (or use --all --kb=<id>)"))
			}
			if opts.DryRun {
				plan := cmdutil.DryRunPlan{
					Action: "doc.delete",
					Args:   map[string]any{"doc_ids": args},
				}
				if opts.All {
					plan.Action = "doc.delete_all"
					plan.Args = map[string]any{"all": true, "kb": opts.KB}
				}
				if handled, err := cmdutil.HandleDryRun(c, true, plan); handled {
					return err
				}
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}

			if opts.All {
				return runDeleteAll(c.Context(), opts, fopts, cli, f.Prompter())
			}
			// Single-id uses the simpler code path (bare {id, deleted}).
			if len(args) == 1 {
				return runDelete(c.Context(), opts, fopts, cli, f.Prompter(), args[0])
			}
			if err := cmdutil.ConfirmDestructiveBatch(f.Prompter(), opts.Yes, fopts.WantsJSON(), "document", len(args), "doc.delete", "weknora doc delete "+strings.Join(args, " ")+" -y"); err != nil {
				return err
			}
			outcomes, runErr := cmdutil.RunBatch(c.Context(), args, func(ctx context.Context, id string) error {
				if err := cli.DeleteKnowledge(ctx, id); err != nil {
					return cmdutil.WrapHTTP(err, "delete document %s", id)
				}
				return nil
			})
			// Only emit when the operation actually ran. Pre-flight errors
			// (e.g. confirmation_required) must leave stdout empty per the
			// wire contract in README.md.
			if len(outcomes) > 0 {
				if emitErr := cmdutil.EmitBatch(outcomes, fopts, iostreams.IO.Out, cmdutil.DeletedAtNow); emitErr != nil {
					return emitErr
				}
			}
			return runErr
		},
	}
	cmd.Flags().BoolVar(&opts.All, "all", false, "delete all documents in the KB specified by --kb")
	cmd.Flags().StringVar(&opts.KB, "kb", "", "knowledge base ID (required with --all)")
	cmdutil.AddFormatFlag(cmd, docDeleteFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetRisk(cmd, "doc.delete")
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "permanently delete one or more documents from a knowledge base",
		RequiredFlags: []string{"<doc-id>... (positional) | --all --kb=<id>"},
		Examples: []string{
			"weknora doc delete doc_abc -y",
			"weknora doc delete doc_a doc_b doc_c -y",
			"weknora doc delete --all --kb=kb_x -y --format json",
		},
		Warnings: []string{
			"Requires explicit user approval (exit 10 / input.confirmation_required); never auto-add -y.",
			"doc delete is irreversible; loses the document + its chunks + embeddings.",
			"--all empties the entire KB; thousands of documents may be lost in one operation.",
		},
	})
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "document", id, "doc.delete", "weknora doc delete "+id+" -y"); err != nil {
		return err
	}

	if err := svc.DeleteKnowledge(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete document %s", id)
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: id, Deleted: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted document %s\n", id)
	return nil
}

// runDeleteAll atomically clears every document in opts.KB via a single
// ClearKnowledgeBaseContents call. Non-TTY/JSON mode without -y returns
// CodeInputConfirmationRequired (exit 10) with risk metadata so agents can
// surface the risk to the user before re-invoking with -y.
func runDeleteAll(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc AllService, p prompt.Prompter) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "all docs in KB", opts.KB, "doc.delete_all", fmt.Sprintf("weknora doc delete --all --kb=%s -y", opts.KB)); err != nil {
		return err
	}

	resp, err := svc.ClearKnowledgeBaseContents(ctx, opts.KB)
	if err != nil {
		return cmdutil.WrapHTTP(err, "clear KB %s", opts.KB)
	}
	deleted := 0
	if resp != nil {
		deleted = resp.DeletedCount
	}

	if !fopts.WantsJSON() {
		fmt.Fprintf(iostreams.IO.Out, "✓ Deleted %d document(s) from KB %s\n", deleted, opts.KB)
		return nil
	}
	return fopts.Emit(iostreams.IO.Out, map[string]any{
		"kb_id":         opts.KB,
		"deleted_count": deleted,
	}, nil)
}
