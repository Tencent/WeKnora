package chunkcmd

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// chunkViewFields enumerates the 23 SDK Chunk fields surfaced for `--format json`
// discovery. JSON is bare SDK pass-through, so keys are snake_case
// (`knowledge_id`, `knowledge_base_id`) even though the human KV labels
// them as `doc_id` / `kb_id`.
var chunkViewFields = []string{
	"id", "seq_id", "knowledge_id", "knowledge_base_id", "tenant_id",
	"tag_id", "content", "chunk_index", "is_enabled", "status",
	"start_at", "end_at", "pre_chunk_id", "next_chunk_id", "chunk_type",
	"parent_chunk_id", "relation_chunks", "indirect_relation_chunks",
	"metadata", "content_hash", "image_info", "created_at", "updated_at",
}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetChunkByIDOnly(ctx context.Context, chunkID string) (*sdk.Chunk, error)
}

type ViewOptions struct {
	ChunkID string
}

const chunkViewLong = `Show a single chunk with all SDK fields.

Text output is a key-value block; pass --format json for the bare 23-field SDK
Chunk object. Content renders verbatim regardless of size — pipe to
less or use --format json for large chunks. WeKnora chunks are typically bounded
by the ingest pipeline (~1000 tokens / a few KB), so unconditional full
rendering is reasonable.

Scope asymmetry with 'chunk delete':
  view <id>           scope-less (server: GET /chunks/by-id/{id})
  delete <id> --doc D scoped     (server: DELETE /chunks/{doc}/{id})

The asymmetry is deliberate. Auto-resolving doc id on delete would race
the ingest pipeline; forcing --doc on view would add friction with no
benefit. AI agents: a chunk id from any source (list, search, agent
invoke citation) is sufficient for view.

Typed exit codes:
  resource.not_found    no chunk with the given id (exit 4)`

const chunkViewExample = `  weknora chunk view chunk_abc
  weknora chunk view chunk_abc --format json | jq '.content'
  weknora chunk view chunk_abc --format json --jq '{id, chunk_index, is_enabled}'`

// NewCmdView builds `weknora chunk view <chunk-id>`.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	opts := &ViewOptions{}
	cmd := &cobra.Command{
		Use:     "view <chunk-id>",
		Short:   "Show a chunk's fields and content (scope-less)",
		Long:    chunkViewLong,
		Example: chunkViewExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			opts.ChunkID = args[0]
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), opts, fopts, cli)
		},
	}
	cmdutil.AddFormatFlag(cmd, chunkViewFields...)
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, fopts *cmdutil.FormatOptions, svc ViewService) error {
	ch, err := svc.GetChunkByIDOnly(ctx, opts.ChunkID)
	if err != nil {
		return cmdutil.WrapHTTP(err, "fetch chunk %s", opts.ChunkID)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, ch, nil)
	}
	renderChunk(iostreams.IO.Out, ch)
	return nil
}

// renderChunk prints a single chunk in human-readable KV form.
// Field order: id / seq_id / chunk_index / doc_id / kb_id / type / enabled /
// status (omit-zero) / start_at (omit-zero) / end_at (omit-zero) /
// tag_id (omit-empty) / image_info (omit-empty) / created_at / updated_at /
// content (full, no truncation, last entry).
//
// `doc_id` / `kb_id` are the human-friendly labels for the SDK fields
// `knowledge_id` / `knowledge_base_id`; JSON output keeps the SDK names.
func renderChunk(w io.Writer, c *sdk.Chunk) {
	fmt.Fprintf(w, "id:           %s\n", c.ID)
	if c.SeqID != 0 {
		fmt.Fprintf(w, "seq_id:       %d\n", c.SeqID)
	}
	fmt.Fprintf(w, "chunk_index:  %d\n", c.ChunkIndex)
	if c.KnowledgeID != "" {
		fmt.Fprintf(w, "doc_id:       %s\n", c.KnowledgeID)
	}
	if c.KnowledgeBaseID != "" {
		fmt.Fprintf(w, "kb_id:        %s\n", c.KnowledgeBaseID)
	}
	if c.ChunkType != "" {
		fmt.Fprintf(w, "type:         %s\n", c.ChunkType)
	}
	enabled := "no"
	if c.IsEnabled {
		enabled = "yes"
	}
	fmt.Fprintf(w, "enabled:      %s\n", enabled)
	if c.Status != 0 {
		fmt.Fprintf(w, "status:       %d\n", c.Status)
	}
	if c.StartAt != 0 {
		fmt.Fprintf(w, "start_at:     %d\n", c.StartAt)
	}
	if c.EndAt != 0 {
		fmt.Fprintf(w, "end_at:       %d\n", c.EndAt)
	}
	if c.TagID != "" {
		fmt.Fprintf(w, "tag_id:       %s\n", c.TagID)
	}
	if c.ImageInfo != "" {
		fmt.Fprintf(w, "image_info:   %s\n", c.ImageInfo)
	}
	if c.CreatedAt != "" {
		fmt.Fprintf(w, "created_at:   %s\n", c.CreatedAt)
	}
	if c.UpdatedAt != "" {
		fmt.Fprintf(w, "updated_at:   %s\n", c.UpdatedAt)
	}
	// Content rendered verbatim, last entry, no truncation.
	fmt.Fprintln(w)
	fmt.Fprintln(w, "content:")
	fmt.Fprintln(w, c.Content)
}
