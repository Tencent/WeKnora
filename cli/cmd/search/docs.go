package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// docsPageSize is the default --page-size on `search docs`: how many
// entries to pull per ListKnowledge round-trip when paging through a KB
// to filter client-side. Server caps page_size at 1000 (per the doc/list
// bound this branch already added). Tunable via --page-size in 1..1000.
const docsPageSize = 200

// docsMaxPageSize bounds the --page-size flag, matching session/doc list canon.
const docsMaxPageSize = 1000

// docsFields enumerates the fields surfaced for `--json` discovery on
// `search docs`. Mirrors sdk.Knowledge json tags.
var docsFields = []string{
	"id", "tenant_id", "knowledge_base_id", "tag_id", "type", "title",
	"description", "source", "channel", "parse_status", "summary_status",
	"enable_status", "embedding_model_id", "file_name", "file_type",
	"file_size", "file_hash", "file_path", "storage_size", "metadata",
	"created_at", "updated_at", "processed_at", "error_message",
}

type DocsSearchOptions struct {
	Query string
	KB    string // raw --kb (UUID or name)
	KBID  string // resolved id; populated before listing
	Limit int
	// PageSize is the server batch size per ListKnowledge call (1..1000,
	// default 200). Tunable so a caller searching a small KB can fetch
	// everything in one round-trip, or a caller on flaky network can
	// shorten the batch.
	PageSize int
	// AllPages walks server pages internally until total exhausted or
	// --limit accumulated. Default true preserves v0.4 behavior; setting
	// false stops after the first page (useful for cheap previews).
	AllPages bool
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
		Long: `Pages through the KB's documents and surfaces every entry whose title or
filename contains the query (case-insensitive). Useful for finding a
specific upload to download or delete by id.

By default, --all-pages=true walks every server page until --limit is
reached or the KB is exhausted (matching v0.4 behavior). Pass
--all-pages=false to stop after one page.`,
		Example: `  weknora search docs "Q3 forecast" --kb finance
  weknora search docs "spec" --kb engineering --limit 5
  weknora search docs "spec" --kb engineering --all-pages=false`,
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
			kbID, err := cmdutil.ResolveKBFlag(c.Context(), cli, opts.KB)
			if err != nil {
				return err
			}
			opts.KBID = kbID
			return runDocsSearch(c.Context(), opts, jopts, cli)
		},
	}
	cmd.Flags().StringVar(&opts.KB, "kb", "", "Knowledge base UUID or name (required)")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", docsPageSize, "Items per server batch (1..1000)")
	cmd.Flags().BoolVar(&opts.AllPages, "all-pages", true, "Walk every server page until exhausted or --limit hit")
	cmdutil.AddJSONFlags(cmd, docsFields)
	_ = cmd.MarkFlagRequired("kb")
	return cmd
}

func runDocsSearch(ctx context.Context, opts *DocsSearchOptions, jopts *cmdutil.JSONOptions, svc DocsSearchService) error {
	if opts.PageSize < 1 || opts.PageSize > docsMaxPageSize {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			fmt.Sprintf("--page-size must be in 1..%d, got %d", docsMaxPageSize, opts.PageSize))
	}
	needle := strings.ToLower(opts.Query)
	var matches []sdk.Knowledge

	// Page through the KB until limit matches found or pagination exhausted.
	// --all-pages=true (default) walks every server page; --all-pages=false
	// stops after the first page. The server returns total; stop when
	// page*pageSize >= total.
	for page := 1; ; page++ {
		items, total, err := svc.ListKnowledge(ctx, opts.KBID, page, opts.PageSize, "")
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
		if !opts.AllPages {
			break
		}
		if int64(page*opts.PageSize) >= total || len(items) == 0 {
			break
		}
	}
done:
	sortKnowledgeByRecency(matches)

	if jopts.Enabled() {
		if matches == nil {
			matches = []sdk.Knowledge{}
		}
		return jopts.Emit(iostreams.IO.Out, matches)
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
