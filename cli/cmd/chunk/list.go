package chunkcmd

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

const (
	defaultPageSize = 50
	maxPageSize     = 1000
	defaultLimit    = 50
	maxLimit        = 1000
	previewWidth    = 80
)

// chunkListFields enumerates the fields surfaced for `--format json` discovery on
// `chunk list`. Mirrors sdk.Chunk json tags — all 23 fields are projectable
// because chunk list returns bare SDK objects.
var chunkListFields = []string{
	"id", "seq_id", "knowledge_id", "knowledge_base_id", "tenant_id",
	"tag_id", "content", "chunk_index", "is_enabled", "status",
	"start_at", "end_at", "pre_chunk_id", "next_chunk_id", "chunk_type",
	"parent_chunk_id", "relation_chunks", "indirect_relation_chunks",
	"metadata", "content_hash", "image_info", "created_at", "updated_at",
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	ListKnowledgeChunks(ctx context.Context, knowledgeID string, page, pageSize int, chunkTypes ...string) ([]sdk.Chunk, int64, error)
}

type ListOptions struct {
	// DocID scopes the listing to a single knowledge document (SDK
	// `knowledge_id`). The chunks SDK does not expose a KB-wide route.
	DocID string
	// PageSize is the server batch size (1..1000, default 50).
	PageSize int
	// Limit caps the client-side accumulated slice (1..1000, default 50).
	// Default 50 chosen as domain-tuned for chunk enumeration (RAG debug).
	Limit int
	// AllPages walks server pages internally until total exhausted or
	// --limit hit. Mirrors the session list / doc list pagination pattern.
	AllPages bool
}

const chunkListLong = `List chunks under a document in stored order.

'chunk list --doc D' enumerates ALL chunks of document D in ChunkIndex
order (the per-doc ordinal assigned at ingest time). This is the
admin/debug surface for RAG retrieval — see what the chunking pipeline
produced, audit content, find a chunk id to view/delete.

For relevance-ranked retrieval (the RAG runtime surface), use
'search chunks "<query>" --kb K' instead. That command runs hybrid
vector + keyword scoring across all chunks of a knowledge base.

Typed exit codes:
  input.invalid_argument   --limit / --page-size out of 1..1000 range (exit 5)
  resource.not_found       no document with the given id (exit 4)

AI agents: prefer 'search chunks' for retrieval tasks. Use 'chunk list'
only when you need to enumerate / verify the chunking output of a
specific document.`

const chunkListExample = `  weknora chunk list --doc doc_abc
  weknora chunk list --doc doc_abc --all-pages --page-size 100
  weknora chunk list --doc doc_abc --format json | jq '.[] | {id, chunk_index}'`

// NewCmdList builds `weknora chunk list --doc <doc-id>`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List chunks of a document (admin/debug, not retrieval)",
		Long:    chunkListLong,
		Example: chunkListExample,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, fopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.DocID, "doc", "", "Document id (SDK knowledge_id) to enumerate chunks for")
	_ = cmd.MarkFlagRequired("doc")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", defaultLimit, "Maximum results to return (1..1000)")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", defaultPageSize, "Items per server batch (1..1000)")
	cmd.Flags().BoolVar(&opts.AllPages, "all-pages", false, "Walk all server pages until exhausted (or --limit hit)")
	cmdutil.AddFormatFlag(cmd, chunkListFields...)
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, fopts *cmdutil.FormatOptions, svc ListService) error {
	if opts.Limit < 1 || opts.Limit > maxLimit {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 1..%d, got %d", maxLimit, opts.Limit),
		}
	}
	if opts.PageSize < 1 || opts.PageSize > maxPageSize {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--page-size must be in 1..%d, got %d", maxPageSize, opts.PageSize),
		}
	}

	var items []sdk.Chunk
	truncated := false
	if opts.AllPages {
		accum := make([]sdk.Chunk, 0)
		for page := 1; ; page++ {
			chunks, total, err := svc.ListKnowledgeChunks(ctx, opts.DocID, page, opts.PageSize)
			if err != nil {
				return cmdutil.WrapHTTP(err, "list chunks for doc %s", opts.DocID)
			}
			accum = append(accum, chunks...)
			if len(accum) >= opts.Limit {
				accum = accum[:opts.Limit]
				truncated = true
				break
			}
			if len(chunks) == 0 || int64(len(accum)) >= total {
				break
			}
		}
		items = accum
	} else {
		chunks, _, err := svc.ListKnowledgeChunks(ctx, opts.DocID, 1, opts.PageSize)
		if err != nil {
			return cmdutil.WrapHTTP(err, "list chunks for doc %s", opts.DocID)
		}
		items = chunks
	}
	if items == nil {
		items = []sdk.Chunk{} // JSON [] not null
	}
	if len(items) > opts.Limit {
		items = items[:opts.Limit]
		truncated = true
	}

	if fopts.WantsJSON() {
		meta := &output.Meta{Count: len(items), HasMore: truncated}
		return fopts.Emit(iostreams.IO.Out, items, meta)
	}

	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no chunks)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHUNK_ID\tINDEX\tTYPE\tENABLED\tPREVIEW\tUPDATED")
	now := time.Now()
	for _, c := range items {
		enabled := "-"
		if c.IsEnabled {
			enabled = "yes"
		}
		preview := text.OneLine(previewWidth, c.Content)
		if preview == "" {
			preview = "-"
		}
		typ := c.ChunkType
		if typ == "" {
			typ = "-"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n",
			c.ID, c.ChunkIndex, typ, enabled, preview, text.FuzzyAgoStr(now, c.UpdatedAt))
	}
	return tw.Flush()
}
