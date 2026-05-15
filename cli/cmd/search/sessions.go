package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// sessionsPageSize is the default --page-size on `search sessions`: how many
// entries to pull per GetSessionsByTenant round-trip when paging through to
// filter client-side. Tunable via --page-size in 1..1000.
const sessionsPageSize = 200

// sessionsMaxPageSize bounds the --page-size flag, matching session/doc list canon.
const sessionsMaxPageSize = 1000

// sessionsSearchFields enumerates the fields surfaced for `--json` discovery
// on `search sessions`. Mirrors sdk.Session json tags.
var sessionsSearchFields = []string{
	"id", "tenant_id", "title", "description", "created_at", "updated_at",
}

type SessionsSearchOptions struct {
	Query string
	Limit int
	// PageSize is the server batch size per GetSessionsByTenant call
	// (1..1000, default 200). Tunable so a caller searching a small set
	// can fetch everything in one round-trip.
	PageSize int
	// AllPages walks server pages internally until total exhausted or
	// --limit accumulated. Default true preserves v0.4 behavior; setting
	// false stops after the first page (useful for cheap previews).
	AllPages bool
}

// SessionsSearchService is the narrow SDK surface this command depends on.
// Server has no session-search endpoint; CLI pages through and filters by
// Title / Description client-side.
type SessionsSearchService interface {
	GetSessionsByTenant(ctx context.Context, page, pageSize int) ([]sdk.Session, int, error)
}

// NewCmdSessions builds `weknora search sessions "<query>"`. Finds chat
// sessions whose title or description contains the query.
func NewCmdSessions(f *cmdutil.Factory) *cobra.Command {
	opts := &SessionsSearchOptions{}
	cmd := &cobra.Command{
		Use:   `sessions "<query>"`,
		Short: "Find chat sessions by title or description (client-side substring match)",
		Long: `Pages through the tenant's chat sessions and surfaces every entry whose
title or description contains the query (case-insensitive).

By default, --all-pages=true walks every server page until --limit is
reached or the tenant's sessions are exhausted (matching v0.4 behavior).
Pass --all-pages=false to stop after one page.`,
		Example: `  weknora search sessions "onboarding"
  weknora search sessions "Q3 review" --limit 3 --json
  weknora search sessions "Q3 review" --all-pages=false`,
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
			return runSessionsSearch(c.Context(), opts, jopts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return")
	cmd.Flags().IntVar(&opts.PageSize, "page-size", sessionsPageSize, "Items per server batch (1..1000)")
	cmd.Flags().BoolVar(&opts.AllPages, "all-pages", true, "Walk every server page until exhausted or --limit hit")
	cmdutil.AddJSONFlags(cmd, sessionsSearchFields)
	return cmd
}

func runSessionsSearch(ctx context.Context, opts *SessionsSearchOptions, jopts *cmdutil.JSONOptions, svc SessionsSearchService) error {
	if opts.PageSize < 1 || opts.PageSize > sessionsMaxPageSize {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			fmt.Sprintf("--page-size must be in 1..%d, got %d", sessionsMaxPageSize, opts.PageSize))
	}
	needle := strings.ToLower(opts.Query)
	var matches []sdk.Session

	// --all-pages=true (default) walks every server page; --all-pages=false
	// stops after the first page.
	for page := 1; ; page++ {
		items, total, err := svc.GetSessionsByTenant(ctx, page, opts.PageSize)
		if err != nil {
			return cmdutil.WrapHTTP(err, "list sessions")
		}
		for _, s := range items {
			if matchSession(s, needle) {
				matches = append(matches, s)
				if opts.Limit > 0 && len(matches) >= opts.Limit {
					goto done
				}
			}
		}
		if !opts.AllPages {
			break
		}
		if page*opts.PageSize >= total || len(items) == 0 {
			break
		}
	}
done:
	sortSessionsByRecency(matches)

	if jopts.Enabled() {
		if matches == nil {
			matches = []sdk.Session{}
		}
		return jopts.Emit(iostreams.IO.Out, matches)
	}
	if len(matches) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no matches)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tUPDATED")
	now := time.Now()
	for _, s := range matches {
		title := text.Truncate(50, s.Title)
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, title, text.FuzzyAgoStr(now, s.UpdatedAt))
	}
	return tw.Flush()
}

// matchSession reports whether title or description contains needle (already
// lowercased by caller).
func matchSession(s sdk.Session, needle string) bool {
	return text.ContainsFold(needle, s.Title, s.Description)
}

// sortSessionsByRecency sorts in place by UpdatedAt desc. Server returns
// strings; we compare lexically - RFC3339 timestamps sort correctly that
// way, and a stable order is enough for output determinism even if a
// non-conforming string slips through.
func sortSessionsByRecency(items []sdk.Session) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
}
