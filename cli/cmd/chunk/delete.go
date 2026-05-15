package chunkcmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// chunkDeleteFields enumerates the JSON discovery fields for `chunk delete`.
// Result payload is a tiny {id, deleted} object — mirrors `kb delete` /
// `agent delete`.
var chunkDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	ChunkID string
	DocID   string // required: SDK DeleteChunk takes both ids in the route.
	Yes     bool   // sourced from the global -y/--yes persistent flag
}

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteChunk(ctx context.Context, docID, chunkID string) error
}

// deleteResult is the typed payload emitted on success in JSON mode.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// Delete is NOT idempotent on a missing id — it surfaces resource.not_found
// (exit 4). Idempotent-already-true semantics are reserved for `unlink`-style
// local cleanups, not server-side resource removal. Mirrors `agent delete`
// and `kb delete`.
const chunkDeleteLong = `Permanently delete a chunk from a document.

Requires both the chunk id (positional) and the parent document id
(--doc) because the server route encodes both: DELETE /chunks/{doc}/{id}.
The CLI does not auto-resolve doc id from the chunk id because doing so
would add a round-trip and open a race with the ingest pipeline (a chunk
could move between documents between resolve and delete).

Prompts for confirmation by default when stdout is a TTY and --json is
not set. Pass -y/--yes (the global flag) to skip the prompt (required in
agent / CI / piped contexts).

Typed exit codes:
  resource.not_found            no chunk with the given id under that doc (exit 4)
  auth.forbidden                caller lacks delete permission on the chunk (exit 3)
  input.confirmation_required   destructive op without -y on a TTY (exit 10)

AI agents: this is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead — the exit-10 protocol exists
exactly to guard against unintended deletes.`

const chunkDeleteExample = `  weknora chunk delete chunk_abc --doc doc_xyz           # interactive confirm
  weknora chunk delete chunk_abc --doc doc_xyz -y        # no prompt
  weknora chunk delete chunk_abc --doc doc_xyz -y --json # bare {id, deleted:true} JSON`

// NewCmdDelete builds `weknora chunk delete <chunk-id> --doc <doc-id>`.
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:     "delete <chunk-id> --doc <doc-id>",
		Short:   "Delete a chunk from a document (scoped)",
		Long:    chunkDeleteLong,
		Example: chunkDeleteExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			opts.ChunkID = args[0]
			opts.Yes, _ = c.Flags().GetBool("yes")
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDelete(c.Context(), opts, jopts, cli, f.Prompter())
		},
	}
	cmd.Flags().StringVar(&opts.DocID, "doc", "", "Parent document id (SDK knowledge_id) the chunk lives under")
	_ = cmd.MarkFlagRequired("doc")
	cmdutil.AddJSONFlags(cmd, chunkDeleteFields)
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, jopts *cmdutil.JSONOptions, svc DeleteService, p prompt.Prompter) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, jopts.Enabled(), "chunk", opts.ChunkID); err != nil {
		return err
	}
	if err := svc.DeleteChunk(ctx, opts.DocID, opts.ChunkID); err != nil {
		return cmdutil.WrapHTTP(err, "delete chunk %s", opts.ChunkID)
	}
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, deleteResult{ID: opts.ChunkID, Deleted: true})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted chunk %s\n", opts.ChunkID)
	return nil
}
