package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

type KBSearchOptions struct {
	Query   string
	Limit   int
	JSONOut bool
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
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runKBSearch(c.Context(), opts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 20, "Maximum results to return")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists KBs whose name or description contains the query (case-insensitive). Useful to discover --kb identifiers before running search chunks / doc list.")
	return cmd
}

func runKBSearch(ctx context.Context, opts *KBSearchOptions, svc KBSearchService) error {
	items, err := svc.ListKnowledgeBases(ctx)
	if err != nil {
		return cmdutil.WrapHTTP(err, "list knowledge bases")
	}
	matches := filterKBs(items, opts.Query)
	if opts.Limit > 0 && len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(matches, nil))
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
