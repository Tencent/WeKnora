package doc

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// docListFields enumerates the fields surfaced for `--json` discovery on
// `doc list`. Filter applies to each Knowledge object in the bare array.
var docListFields = []string{
	"id", "knowledge_base_id", "tag_id", "type", "title", "description",
	"source", "channel", "parse_status", "summary_status", "enable_status",
	"embedding_model_id", "file_name", "file_type", "file_size", "file_hash",
	"file_path", "storage_size",
	"created_at", "updated_at", "processed_at", "error_message",
}

type ListOptions struct {
	PageSize int    // Items per server batch. With --all-pages, controls
	                // per-request load. Without, controls the single page size.
	Status   string // --status: filter by parse_status (server-side query param)
	// Limit caps the returned items client-side (default 30; 0 = no cap).
	// Applied after pagination / --all-pages accumulation and sort.
	Limit int
	// AllPages walks server pages internally, accumulating items until
	// total exhausted or --limit hit.
	AllPages bool
}

// docListStatusValues mirrors internal/types/knowledge.go ParseStatus*
// constants — these are the values the server accepts on the
// ?parse_status= query. Kept in sync manually since the SDK doesn't
// re-export the enum.
var docListStatusValues = []string{"pending", "processing", "completed", "failed"}

// ListService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type ListService interface {
	ListKnowledgeWithFilter(ctx context.Context, kbID string, page, pageSize int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error)
}

// NewCmdList builds `weknora doc list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documents in a knowledge base",
		Long: `Lists documents (uploaded files / web pages / inline text) in the
resolved knowledge base. KB resolution follows the standard 4-level chain:
--kb flag > WEKNORA_KB_ID env > .weknora/project.yaml > error. The --kb
flag accepts either a KB UUID (passed through) or a name (resolved via list).

Default sort is updated_at desc so the most recent uploads surface first;
backend storage order is not guaranteed and varies between deployments.`,
		Example: `  weknora doc list                                                  # uses project link / env
  weknora doc list --kb a32a63ff-fb36-4874-bcaa-30f48570a694        # explicit UUID
  weknora doc list --kb my-kb                                       # resolved by name
  weknora doc list --all-pages --json                               # walk every page`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, jopts, cli, kbID)
		},
	}
	// --kb is read by Factory.ResolveKB; declare it here so cobra parses the
	// value into the command's flag set.
	cmd.Flags().String("kb", "", "Knowledge base UUID or name (overrides env / project link)")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", 50, "Items per server batch (1..1000)")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return (0 = no cap, 1..10000 = explicit)")
	cmd.Flags().BoolVar(&opts.AllPages, "all-pages", false, "Walk all server pages until exhausted (or --limit hit)")
	cmd.Flags().StringVar(&opts.Status, "status", "", "Filter by parse status: pending | processing | completed | failed")
	cmdutil.AddJSONFlags(cmd, docListFields)
	aiclient.SetAgentHelp(cmd, "Lists docs in the resolved KB as a bare JSON array of Knowledge objects (empty `[]` when none). --status filters server-side; `failed` surfaces ingestion errors. --all-pages walks every server page until exhausted (capped by --limit), useful for one-shot exports.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, jopts *cmdutil.JSONOptions, svc ListService, kbID string) error {
	if opts.PageSize < 1 || opts.PageSize > 1000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--page-size must be in 1..1000, got %d", opts.PageSize),
		}
	}
	if opts.Limit < 0 || opts.Limit > 10000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 0..10000 (0 = no cap), got %d", opts.Limit),
		}
	}
	if opts.Status != "" && !validDocListStatus(opts.Status) {
		return &cmdutil.Error{
			Code: cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--status must be one of: %s — got %q",
				strings.Join(docListStatusValues, " | "), opts.Status),
		}
	}
	filter := sdk.KnowledgeListFilter{ParseStatus: opts.Status}

	// Pagination is always 1-indexed internally. --all-pages walks; the
	// non-walking path returns the first page only.
	var (
		items []sdk.Knowledge
		total int64
	)
	if opts.AllPages {
		accum := make([]sdk.Knowledge, 0)
		page := 1
		for {
			chunk, t, err := svc.ListKnowledgeWithFilter(ctx, kbID, page, opts.PageSize, filter)
			if err != nil {
				return cmdutil.WrapHTTP(err, "list documents")
			}
			total = t
			accum = append(accum, chunk...)
			if opts.Limit > 0 && len(accum) >= opts.Limit {
				accum = accum[:opts.Limit]
				break
			}
			if int64(page*opts.PageSize) >= total || len(chunk) == 0 {
				break
			}
			page++
		}
		items = accum
	} else {
		chunk, t, err := svc.ListKnowledgeWithFilter(ctx, kbID, 1, opts.PageSize, filter)
		if err != nil {
			return cmdutil.WrapHTTP(err, "list documents")
		}
		items = chunk
		total = t
	}
	if items == nil {
		items = []sdk.Knowledge{} // ensure JSON [] not null
	}
	// Default sort: updated_at desc. Server return order is not guaranteed,
	// so client-side sort makes output deterministic regardless of backend
	// storage choices. Mirrors `weknora kb list`.
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	// --limit applies after sort so users get the top-N most-recent items
	// when combined with a single-page fetch where page_size > limit.
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	_ = total // pagination metadata is no longer surfaced; --all-pages drains for callers who need everything

	if jopts.Enabled() {
		return format.WriteJSONFiltered(iostreams.IO.Out, items, jopts.Fields, jopts.JQ)
	}

	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no documents)")
		return nil
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tSIZE\tUPDATED")
	now := time.Now()
	for _, k := range items {
		name := text.Truncate(40, text.KnowledgeDisplayName(k.FileName, k.Title, k.ID))
		updated := text.FuzzyAgo(now, k.UpdatedAt)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", k.ID, name, k.ParseStatus, formatSize(k.FileSize), updated)
	}
	return tw.Flush()
}

// validDocListStatus reports whether s matches one of the server-accepted
// parse_status enum values surfaced via --status.
func validDocListStatus(s string) bool {
	return slices.Contains(docListStatusValues, s)
}

// formatSize renders a byte count as a short human string (KB / MB).
// Kept tiny on purpose — go-humanize would pull a transitive dep just for one
// column. A "-" placeholder hides zero-size entries (URL / text).
func formatSize(bytes int64) string {
	if bytes <= 0 {
		return "-"
	}
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	}
	return fmt.Sprintf("%dB", bytes)
}
