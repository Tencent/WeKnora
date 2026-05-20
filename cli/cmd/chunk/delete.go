package chunkcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// chunkDeleteFields enumerates the JSON discovery fields for `chunk delete`.
// Tracks the single-id result struct; multi-id mode emits MultiDeleteResult.
var chunkDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	ChunkID string // single-id path
	DocID   string // required: SDK DeleteChunk takes both ids in the route
	Yes     bool   // sourced from the global -y/--yes persistent flag
}

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteChunk(ctx context.Context, docID, chunkID string) error
}

// deleteResult is the typed payload emitted on single-id success in JSON mode.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// MultiDeleteResult is the payload for multi-id deletes. All chunks share the
// same --doc parent (server route is DELETE /chunks/{doc}/{id}).
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

const chunkDeleteLong = `Permanently delete one or more chunks from a document.

Requires both the chunk id(s) (positional, repeatable) and the parent
document id (--doc) because the server route encodes both:
DELETE /chunks/{doc}/{id}. All chunks in a multi-id call must share the
same --doc. The CLI does not auto-resolve doc id from chunk id because
that would add a round-trip and open a race with the ingest pipeline
(a chunk could move between documents between resolve and delete).

Single-id: one confirm prompt, exit 0/1.
Multi-id:
  • Default keep-going: failed deletes do NOT stop the run; failures collected.
  • One -y/--yes confirms all chunks.
  • Exit 0 if all succeed; exit 1 if any failed.

Prompts for confirmation by default when stdout is a TTY and JSON output
is not set. Pass -y/--yes (the global flag) to skip the prompt (required
in agent / CI / piped contexts).

Typed exit codes:
  resource.not_found            no chunk with the given id under that doc (exit 4)
  auth.forbidden                caller lacks delete permission on the chunk (exit 3)
  input.confirmation_required   destructive op without -y on a TTY (exit 10)

AI agents: this is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead — the exit-10 protocol exists
exactly to guard against unintended deletes.`

const chunkDeleteExample = `  weknora chunk delete chunk_abc --doc doc_xyz                  # interactive confirm
  weknora chunk delete chunk_abc --doc doc_xyz -y               # no prompt
  weknora chunk delete chunk_abc --doc doc_xyz -y --format json # bare {id, deleted:true} JSON
  weknora chunk delete c1 c2 c3 --doc doc_xyz -y                # delete 3 chunks under same doc, keep-going`

// NewCmdDelete builds `weknora chunk delete <chunk-id> [<chunk-id>...] --doc <doc-id>`.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:     "delete <chunk-id> [<chunk-id>...] --doc <doc-id>",
		Short:   "Delete one or more chunks from a document (scoped)",
		Long:    chunkDeleteLong,
		Example: chunkDeleteExample,
		Args:    cobra.MinimumNArgs(1),
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
			if len(args) == 1 {
				opts.ChunkID = args[0]
				return runDelete(c.Context(), opts, fopts, cli, f.Prompter())
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
	cmd.Flags().StringVar(&opts.DocID, "doc", "", "Parent document id (SDK knowledge_id) the chunks live under")
	_ = cmd.MarkFlagRequired("doc")
	cmdutil.AddFormatFlag(cmd, chunkDeleteFields...)
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "chunk", opts.ChunkID); err != nil {
		return err
	}
	if err := svc.DeleteChunk(ctx, opts.DocID, opts.ChunkID); err != nil {
		return cmdutil.WrapHTTP(err, "delete chunk %s", opts.ChunkID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: opts.ChunkID, Deleted: true})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted chunk %s\n", opts.ChunkID)
	return nil
}

// runMultiDelete iterates chunkIDs sequentially under opts.DocID, keep-going
// on error: a single failure does not abort the run.
func runMultiDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter, chunkIDs []string) (*MultiDeleteResult, error) {
	if err := cmdutil.ConfirmDestructiveBatch(p, opts.Yes, fopts.WantsJSON(), "chunk", len(chunkIDs)); err != nil {
		return &MultiDeleteResult{}, err
	}
	res := &MultiDeleteResult{}
	for _, id := range chunkIDs {
		if err := svc.DeleteChunk(ctx, opts.DocID, id); err != nil {
			res.Failed = append(res.Failed, FailedItem{ID: id, Message: err.Error()})
			continue
		}
		res.OK = append(res.OK, id)
	}
	if len(res.Failed) > 0 {
		return res, cmdutil.NewError(cmdutil.CodeOperationFailed, fmt.Sprintf("%d/%d delete(s) failed", len(res.Failed), len(chunkIDs)))
	}
	return res, nil
}

// emitMultiDelete renders per --format. Mirrors doc / session delete.
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
		return fmt.Errorf("unsupported --format %q for chunk delete", fopts.Mode)
	}
}
