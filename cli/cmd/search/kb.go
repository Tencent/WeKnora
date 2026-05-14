package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbSearchFields enumerates the fields surfaced for `--json` discovery on
// `search kb`. Subset of KnowledgeBase suitable for list/filter results.
var kbSearchFields = []string{
	"id", "name", "type", "description",
	"is_temporary", "is_pinned",
	"embedding_model_id", "summary_model_id",
	"knowledge_count", "chunk_count",
	"is_processing", "processing_count",
	"created_at", "updated_at",
}

type KBSearchOptions struct {
	Query string
	Limit int
}

// kbSearchResult is the typed payload emitted under data.items.
type kbSearchResult struct {
	Items []sdk.KnowledgeBase `json:"items"`
}

// KBSearchService is the narrow SDK surface this command depends on.
// Server has no fuzzy-KB-name endpoint; the CLI filters ListKnowledgeBases
// client-side. Acceptable because tenants typically have ≪ 1000 KBs.
type KBSearchService interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
}

// NewCmdKB builds `weknora search kb "<query>"` — substring + case-insensitive
// match across KB names and descriptions visible to the active context.
// Results are sorted by name length (shortest first; usually the closest
// hit) for deterministic output.
func NewCmdKB(f *cmdutil.Factory) *cobra.Command {
	opts := &KBSearchOptions{}
	cmd := &cobra.Command{
		Use:   `kb "<query>"`,
		Short: "Find knowledge bases by name or description (client-side substring match)",
		Example: `  weknora search kb "marketing"
  weknora search kb "team" --limit 5 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(args[0])
			if opts.Query == "" {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
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
			return runKBSearch(c.Context(), opts, jopts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return")
	cmdutil.AddJSONFlags(cmd, kbSearchFields)
	aiclient.SetAgentHelp(cmd, "Lists KBs whose name or description contains the query (case-insensitive). Useful to discover --kb identifiers before running search chunks / doc list.")
	return cmd
}

func runKBSearch(ctx context.Context, opts *KBSearchOptions, jopts *cmdutil.JSONOptions, svc KBSearchService) error {
	items, err := svc.ListKnowledgeBases(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list knowledge bases")
	}
	matches := filterKBs(items, opts.Query)
	if opts.Limit > 0 && len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}

	if jopts.Enabled() {
		if matches == nil {
			matches = []sdk.KnowledgeBase{}
		}
		return format.WriteEnvelopeFiltered(
			iostreams.IO.Out,
			format.Success(kbSearchResult{Items: matches}, nil),
			jopts.Fields, jopts.JQ,
		)
	}
	if len(matches) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no matches)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tDOCS")
	for _, kb := range matches {
		name := text.Truncate(50, kb.Name)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", kb.ID, name, text.Pluralize(int(kb.KnowledgeCount), "doc"))
	}
	return tw.Flush()
}

// filterKBs returns the KBs whose name or description contains q (case-
// insensitive), sorted by name length so the most-likely match shows
// first. Ties broken alphabetically for determinism.
func filterKBs(items []sdk.KnowledgeBase, q string) []sdk.KnowledgeBase {
	needle := strings.ToLower(q)
	out := make([]sdk.KnowledgeBase, 0, len(items))
	for _, kb := range items {
		if text.ContainsFold(needle, kb.Name, kb.Description) {
			out = append(out, kb)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Name) != len(out[j].Name) {
			return len(out[i].Name) < len(out[j].Name)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
