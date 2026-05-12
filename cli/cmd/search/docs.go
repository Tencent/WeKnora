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

// docsPageSize is how many entries we pull per ListKnowledge round-trip
// when paging through a KB to filter client-side. Server caps page_size at
// 1000 (per the doc/list bound this branch already added).
const docsPageSize = 200

type DocsSearchOptions struct {
	Query   string
	KB      string // raw --kb (UUID or name)
	KBID    string // resolved id; populated before listing
	Limit   int
	JSONOut bool
}

// DocsSearchService is the narrow SDK surface this command depends on.
// Server has no fuzzy-document-name endpoint, so the CLI pages through
// ListKnowledge and filters by Title / FileName client-side.
type DocsSearchService interface {
	ListKnowledge(ctx context.Context, kbID string, page, pageSize int, tagID string) ([]sdk.Knowledge, int64, error)
}

// NewCmdDocs builds `weknora search docs "<query>" --kb <id-or-name>`.
// Pages through the KB's documents and surfaces every entry whose title
// or filename contains the query (case-insensitive). Useful for finding
// a specific upload to download or delete.
func NewCmdDocs(f *cmdutil.Factory) *cobra.Command {
	opts := &DocsSearchOptions{}
	cmd := &cobra.Command{
		Use:   `docs "<query>"`,
		Short: "Find documents in a knowledge base by name (client-side substring match)",
		Example: `  weknora search docs "Q3 forecast" --kb finance
  weknora search docs "spec" --kb engineering --limit 5`,
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
			kbID, err := cmdutil.ResolveKBFlag(c.Context(), cli, opts.KB)
			if err != nil {
				return err
			}
			opts.KBID = kbID
			return runDocsSearch(c.Context(), opts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.KB, "kb", "", "Knowledge base UUID or name (required)")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 20, "Maximum results to return")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	_ = cmd.MarkFlagRequired("kb")
	agent.SetAgentHelp(cmd, "Lists documents in --kb whose title or file_name contains the query. Pages through the KB sequentially; stops once limit hits found. Returns the full Knowledge object so agents can derive id / file_size / processed_at without a second call.")
	return cmd
}

func runDocsSearch(ctx context.Context, opts *DocsSearchOptions, svc DocsSearchService) error {
	needle := strings.ToLower(opts.Query)
	var matches []sdk.Knowledge

	// Page through the KB until limit matches found or pagination exhausted.
	// The server returns total; stop when (page-1)*pageSize >= total.
	for page := 1; ; page++ {
		items, total, err := svc.ListKnowledge(ctx, opts.KBID, page, docsPageSize, "")
		if err != nil {
			return cmdutil.WrapHTTP(err, "list documents")
		}
		for _, k := range items {
			if matchKnowledge(k, needle) {
				matches = append(matches, k)
				if opts.Limit > 0 && len(matches) >= opts.Limit {
					goto done
				}
			}
		}
		if int64(page*docsPageSize) >= total || len(items) == 0 {
			break
		}
	}
done:
	sortKnowledgeByRecency(matches)

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(matches, &format.Meta{KBID: opts.KBID}))
	}
	if len(matches) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no matches)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFILE\tTYPE\tUPDATED")
	for _, k := range matches {
		name := text.Truncate(50, text.KnowledgeDisplayName(k.FileName, k.Title, k.ID))
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", k.ID, name, k.FileType, k.UpdatedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}

// matchKnowledge reports whether title or filename contains needle (already
// lowercased by caller).
func matchKnowledge(k sdk.Knowledge, needle string) bool {
	return text.ContainsFold(needle, k.Title, k.FileName)
}

// sortKnowledgeByRecency sorts in place by UpdatedAt desc.
func sortKnowledgeByRecency(items []sdk.Knowledge) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}
