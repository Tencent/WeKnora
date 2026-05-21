package chunkcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// chunkDeleteFields enumerates the JSON discovery fields for `chunk delete`.
// Tracks the single-id result struct; multi-id mode emits batch envelope.
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
			if err := cmdutil.ConfirmDestructiveBatch(f.Prompter(), opts.Yes, fopts.WantsJSON(), "chunk", len(args), "chunk.delete", "weknora chunk delete "+strings.Join(args, " ")+" --doc "+opts.DocID+" -y"); err != nil {
				return err
			}
			outcomes, runErr := cmdutil.RunBatch(c.Context(), args, func(ctx context.Context, id string) error {
				if err := cli.DeleteChunk(ctx, opts.DocID, id); err != nil {
					return cmdutil.WrapHTTP(err, "delete chunk %s", id)
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
	cmd.Flags().StringVar(&opts.DocID, "doc", "", "Parent document id (SDK knowledge_id) the chunks live under")
	_ = cmd.MarkFlagRequired("doc")
	cmdutil.AddFormatFlag(cmd, chunkDeleteFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "permanently delete one or more chunks from a document",
		RequiredFlags: []string{"<chunk-id>... (positional, at least one)", "--doc <doc-id>"},
		Examples: []string{
			"weknora chunk delete chunk_abc --doc doc_xyz -y",
			"weknora chunk delete c1 c2 c3 --doc doc_xyz -y",
			"weknora chunk delete chunk_abc --doc doc_xyz -y --format json",
		},
		Warnings: []string{
			"chunk delete removes a chunk from a doc permanently. Never auto-add -y unless the user explicitly approves the specific chunk id.",
		},
	})
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "chunk", opts.ChunkID, "chunk.delete", "weknora chunk delete "+opts.ChunkID+" --doc "+opts.DocID+" -y"); err != nil {
		return err
	}
	if err := svc.DeleteChunk(ctx, opts.DocID, opts.ChunkID); err != nil {
		return cmdutil.WrapHTTP(err, "delete chunk %s", opts.ChunkID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: opts.ChunkID, Deleted: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted chunk %s\n", opts.ChunkID)
	return nil
}
