package doc

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// docDeleteFields enumerates the fields surfaced for `--format json` discovery
// on `doc delete`. The result payload is a small {id, deleted} object.
var docDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	Yes bool // sourced from the global -y/--yes persistent flag (see cli/cmd/root.go)
}

// DeleteService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type DeleteService interface {
	DeleteKnowledge(ctx context.Context, id string) error
}

// deleteResult is the typed payload emitted under data on success (single-id).
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// MultiDeleteResult is the payload for multi-id deletes.
// ok: ids successfully deleted; failed: ids that could not be deleted.
type MultiDeleteResult struct {
	OK     []string     `json:"ok"`
	Failed []FailedItem `json:"failed,omitempty"`
}

// FailedItem records an id that failed to delete along with its error message.
type FailedItem struct {
	ID      string `json:"id"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// NewCmdDelete builds `weknora doc delete`. Single-id keeps the simpler
// code path (one confirm prompt, exit 0/1); multi-id uses keep-going
// semantics (one -y confirms all, failures collected, exit 1 if any fail).
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <doc-id> [<doc-id>...]",
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

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead.`,
		Example: `  weknora doc delete doc_abc                   # interactive confirm
  weknora doc delete doc_abc -y                # no prompt
  weknora doc delete doc_abc -y --format json  # bare {id, deleted:true} JSON
  weknora doc delete doc_a doc_b doc_c -y      # delete 3, keep-going
  weknora doc delete doc_a doc_b --format json # multi-id JSON output`,
		Args: cobra.MinimumNArgs(1),
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
			// Single-id uses the simpler code path (bare {id, deleted}).
			if len(args) == 1 {
				return runDelete(c.Context(), opts, fopts, cli, f.Prompter(), args[0])
			}
			res, runErr := runMultiDelete(c.Context(), opts, fopts, cli, f.Prompter(), args)
			// Only emit when the operation actually ran. Pre-flight errors
			// (e.g. confirmation_required) must leave stdout empty per the
			// wire contract in README.md.
			if len(res.OK) > 0 || len(res.Failed) > 0 {
				if emitErr := emitMultiDelete(res, fopts, iostreams.IO.Out); emitErr != nil {
					return emitErr
				}
			}
			return runErr
		},
	}
	cmdutil.AddFormatFlag(cmd, docDeleteFields...)
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "document", id); err != nil {
		return err
	}

	if err := svc.DeleteKnowledge(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete document %s", id)
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: id, Deleted: true})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted document %s\n", id)
	return nil
}

// runMultiDelete iterates ids sequentially, keep-going on error: a single
// failure does not abort the run, so the caller sees the full outcome.
func runMultiDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter, ids []string) (*MultiDeleteResult, error) {
	if err := cmdutil.ConfirmDestructiveBatch(p, opts.Yes, fopts.WantsJSON(), "document", len(ids)); err != nil {
		return &MultiDeleteResult{}, err
	}
	res := &MultiDeleteResult{}
	for _, id := range ids {
		if err := svc.DeleteKnowledge(ctx, id); err != nil {
			res.Failed = append(res.Failed, FailedItem{ID: id, Message: err.Error()})
			continue
		}
		res.OK = append(res.OK, id)
	}
	if len(res.Failed) > 0 {
		return res, cmdutil.NewError(cmdutil.CodeOperationFailed, fmt.Sprintf("%d/%d delete(s) failed", len(res.Failed), len(ids)))
	}
	return res, nil
}

// emitMultiDelete renders per --format. Mirrors emitWaitResult / emitStatus.
func emitMultiDelete(res *MultiDeleteResult, fopts *cmdutil.FormatOptions, w io.Writer) error {
	switch fopts.Mode {
	case cmdutil.FormatJSON, cmdutil.FormatNDJSON:
		return fopts.Emit(w, res)
	case cmdutil.FormatText, "":
		for _, id := range res.OK {
			fmt.Fprintf(w, "OK %s\n", id)
		}
		for _, f := range res.Failed {
			fmt.Fprintf(w, "FAIL %s: %s\n", f.ID, f.Message)
		}
		return nil
	default:
		return fmt.Errorf("unsupported --format %q for doc delete", fopts.Mode)
	}
}
