package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// chunksFields enumerates the fields surfaced for `--json` discovery on
// `search chunks`. Lists data.items[*] (SearchResult) fields.
var chunksFields = []string{
	"id", "content", "knowledge_id", "chunk_index", "knowledge_title",
	"start_at", "end_at", "seq", "score", "match_type", "chunk_type",
	"image_info", "metadata", "knowledge_filename", "knowledge_source",
	"knowledge_channel", "matched_content",
}

type ChunksOptions struct {
	Query            string
	KB               string // raw --kb (UUID or name)
	KBID             string // resolved id; populated before HybridSearch
	Limit            int
	VectorThreshold  float64
	KeywordThreshold float64
	NoVector         bool
	NoKeyword        bool
}

// chunksResult is the typed payload emitted under data.items.
type chunksResult struct {
	Items []*sdk.SearchResult `json:"items"`
}

// ChunksService is the narrow SDK surface used by runChunks. *sdk.Client
// satisfies it; tests inject fakes via Factory.Client.
type ChunksService interface {
	HybridSearch(ctx context.Context, kbID string, params *sdk.SearchParams) ([]*sdk.SearchResult, error)
}

// NewCmdChunks builds `weknora search chunks "<query>" --kb <id-or-name>`.
// Uses a positional query argument with the KB selected via flag.
//
// The `--kb` flag accepts either a KB UUID (passed through unchanged) or a
// name (resolved via ListKnowledgeBases — see cmdutil.ResolveKBFlag).
func NewCmdChunks(f *cmdutil.Factory) *cobra.Command {
	opts := &ChunksOptions{}
	cmd := &cobra.Command{
		Use:   `chunks "<query>"`,
		Short: "Hybrid (vector + keyword) chunk retrieval against a knowledge base",
		Example: `  weknora search chunks "what is RAG?" --kb engineering
  weknora search chunks "embedding model" --kb kb_abc --limit 20
  weknora search chunks "k8s" --kb engineering --no-keyword         # vector-only`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(args[0])
			if err := opts.validate(); err != nil {
				return err
			}
			if opts.Limit < 1 || opts.Limit > 1000 {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--limit must be between 1 and 1000")
			}
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			kbID, err := cmdutil.ResolveKBFlag(c.Context(), cli, opts.KB)
			if err != nil {
				return err
			}
			opts.KBID = kbID
			return runChunks(c.Context(), opts, jopts, cli)
		},
	}
	bindChunksFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("kb")
	aiclient.SetAgentHelp(cmd, "Hybrid retrieval; returns ranked chunk list. The server may include parent/nearby/relation chunks beyond match_count; --limit caps the returned slice client-side. Pass --no-vector or --no-keyword to disable a channel (mutually exclusive both-off).")
	return cmd
}

// bindChunksFlags registers the chunks flag surface in one place to keep
// the constructor readable; --kb is marked required by the caller.
func bindChunksFlags(cmd *cobra.Command, opts *ChunksOptions) {
	cmd.Flags().StringVar(&opts.KB, "kb", "", "Knowledge base UUID or name")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 8, "Maximum results to return (default 8 — tuned for RAG context window; list commands default to 30)")
	cmd.Flags().Float64Var(&opts.VectorThreshold, "vector-threshold", 0, "Vector retrieval similarity floor (per-channel, pre-fusion); 0 = no filter")
	cmd.Flags().Float64Var(&opts.KeywordThreshold, "keyword-threshold", 0, "Keyword retrieval score floor (per-channel, pre-fusion); 0 = no filter")
	cmd.Flags().BoolVar(&opts.NoVector, "no-vector", false, "Disable the vector channel")
	cmd.Flags().BoolVar(&opts.NoKeyword, "no-keyword", false, "Disable the keyword channel")
	cmdutil.AddJSONFlags(cmd, chunksFields)
}

// validate checks the option set before any SDK call. Limit bounds are
// enforced separately in RunE (user-input boundary) so internal callers
// can pass Limit==0 for the "no client-side cap" path.
func (o *ChunksOptions) validate() error {
	if o.Query == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
	}
	if o.NoVector && o.NoKeyword {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--no-vector and --no-keyword cannot both be set")
	}
	return nil
}

func runChunks(ctx context.Context, opts *ChunksOptions, jopts *cmdutil.JSONOptions, svc ChunksService) error {
	if err := opts.validate(); err != nil {
		return err
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "search chunks: no SDK client available")
	}

	params := &sdk.SearchParams{
		QueryText:            opts.Query,
		MatchCount:           opts.Limit,
		VectorThreshold:      opts.VectorThreshold,
		KeywordThreshold:     opts.KeywordThreshold,
		DisableVectorMatch:   opts.NoVector,
		DisableKeywordsMatch: opts.NoKeyword,
	}
	results, err := svc.HybridSearch(ctx, opts.KBID, params)
	if err != nil {
		return cmdutil.WrapHTTP(err, "hybrid search")
	}
	// match_count is the server's *primary-match* cap — after that, the
	// service appends parent / nearby / relation chunks as context
	// enrichment, so the wire response can exceed Limit. Treat --limit as
	// a hard return-count cap by trimming on the client. Recall isn't
	// affected because the server's internal retrieval pool is already
	// max(MatchCount*5, 50).
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	if jopts.Enabled() {
		return format.WriteEnvelopeFiltered(
			iostreams.IO.Out,
			format.Success(chunksResult{Items: results}, &format.Meta{KBID: opts.KBID}),
			jopts.Fields, jopts.JQ,
		)
	}
	return renderChunkResults(results, opts.KBID)
}

// renderChunkResults prints a compact pretty list. Minimal stopgap — a
// richer tabular renderer can replace this later without breaking the
// JSON contract.
func renderChunkResults(results []*sdk.SearchResult, kbID string) error {
	if len(results) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no results)")
		return nil
	}
	fmt.Fprintf(iostreams.IO.Out, "%d result(s) from kb=%s:\n\n", len(results), kbID)
	for i, r := range results {
		fmt.Fprintf(iostreams.IO.Out, "[%d] score=%.3f", i+1, r.Score)
		if r.KnowledgeID != "" {
			fmt.Fprintf(iostreams.IO.Out, "  doc=%s", r.KnowledgeID)
		}
		fmt.Fprintln(iostreams.IO.Out)
		fmt.Fprintln(iostreams.IO.Out, indent(strings.TrimSpace(r.Content), "    "))
		fmt.Fprintln(iostreams.IO.Out)
	}
	return nil
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
