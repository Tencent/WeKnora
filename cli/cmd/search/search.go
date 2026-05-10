// Package search implements the top-level `weknora search` command — the
// chunk hybrid retrieval entry point (ADR-3: only one search command).
package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// Options is the runtime configuration of one search invocation.
type Options struct {
	Query            string
	KB               string // raw --kb flag value (UUID or name)
	KBID             string // resolved id, populated by RunE before HybridSearch
	TopK             int
	VectorThreshold  float64
	KeywordThreshold float64
	NoVector         bool
	NoKeyword        bool
	JSONOut          bool
}

// Service is the narrow SDK surface used by runSearch. *sdk.Client satisfies
// it; tests inject fakes via Factory.Client.
type Service interface {
	HybridSearch(ctx context.Context, kbID string, params *sdk.SearchParams) ([]*sdk.SearchResult, error)
}

// NewCmdSearch builds `weknora search "<query>" --kb <id-or-name>`.
//
// The single `--kb` flag accepts either a KB UUID (passed through) or a
// name (resolved via ListKnowledgeBases). Mirrors gcloud `--project`'s
// id-or-name auto-detection — the only mainstream pattern that collapses
// the two forms onto one flag.
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   `search "<query>"`,
		Short: "Hybrid (vector + keyword) chunk retrieval against a knowledge base",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(args[0])
			// Validate input shape before touching the SDK so flag/arg misuse
			// surfaces fast (no auth / no client construction). KB resolution
			// happens *after* — name → id needs a live client.
			if err := opts.validate(); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			if cmdutil.IsKBID(opts.KB) {
				opts.KBID = opts.KB
			} else {
				resolved, rerr := cmdutil.ResolveKBNameToID(c.Context(), cli, opts.KB)
				if rerr != nil {
					return rerr
				}
				opts.KBID = resolved
			}
			return runSearch(c.Context(), opts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.KB, "kb", "", "Knowledge base UUID or name")
	cmd.Flags().IntVar(&opts.TopK, "top-k", 8, "Maximum results to return")
	cmd.Flags().Float64Var(&opts.VectorThreshold, "vector-threshold", 0, "Vector retrieval similarity floor (per-channel, pre-fusion); 0 = no filter")
	cmd.Flags().Float64Var(&opts.KeywordThreshold, "keyword-threshold", 0, "Keyword retrieval score floor (per-channel, pre-fusion); 0 = no filter")
	cmd.Flags().BoolVar(&opts.NoVector, "no-vector", false, "Disable the vector channel")
	cmd.Flags().BoolVar(&opts.NoKeyword, "no-keyword", false, "Disable the keyword channel")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	_ = cmd.MarkFlagRequired("kb")
	return cmd
}

// validate checks the option set before any SDK call. RunE invokes it before
// f.Client() so flag misuse fails fast; runSearch re-invokes it as a safety
// net for direct test calls that bypass RunE.
func (o *Options) validate() error {
	if o.Query == "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
	}
	if o.NoVector && o.NoKeyword {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "--no-vector and --no-keyword cannot both be set")
	}
	return nil
}

func runSearch(ctx context.Context, opts *Options, svc Service) error {
	if err := opts.validate(); err != nil {
		return err
	}
	if svc == nil {
		return cmdutil.NewError(cmdutil.CodeServerError, "search: no SDK client available")
	}

	params := &sdk.SearchParams{
		QueryText:            opts.Query,
		MatchCount:           opts.TopK,
		VectorThreshold:      opts.VectorThreshold,
		KeywordThreshold:     opts.KeywordThreshold,
		DisableVectorMatch:   opts.NoVector,
		DisableKeywordsMatch: opts.NoKeyword,
	}
	results, err := svc.HybridSearch(ctx, opts.KBID, params)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "hybrid search")
	}
	// match_count is the server's *primary-match* cap — after that, the
	// service appends parent / nearby / relation chunks as context
	// enrichment, so the wire response can exceed TopK. CLIs like gh /
	// kubectl / aws treat their `--limit`-style flag as a hard return-count
	// cap; honor that contract by trimming on the client. Recall isn't
	// affected because the server's internal retrieval pool is already
	// max(MatchCount*5, 50).
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}

	if opts.JSONOut {
		return cmdutil.NewJSONExporter().Write(iostreams.IO.Out, format.Success(results, &format.Meta{
			KBID: opts.KBID,
		}))
	}
	return renderHumanResults(results, opts.KBID)
}

// renderHumanResults prints a compact pretty list to stdout. The inline
// indent helper is a minimal stopgap so search output is usable in a plain
// terminal; a richer tabular renderer can replace this later.
func renderHumanResults(results []*sdk.SearchResult, kbID string) error {
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

// indent prefixes each line of s with the given prefix.
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
