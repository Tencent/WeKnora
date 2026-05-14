package sessioncmd

import (
	"context"
	"fmt"
	"strconv"
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

const (
	defaultPage     = 1
	defaultPageSize = 30
	maxPageSize     = 1000
)

// sessionListFields enumerates the fields surfaced for `--json` discovery on
// `session list`. Mirrors sdk.Session json tags.
var sessionListFields = []string{
	"id", "tenant_id", "title", "description", "created_at", "updated_at",
}

type ListOptions struct {
	PageSize int    // Items per server batch (default 50).
	Since    string // --since: filter to sessions updated within the past duration
	// Limit caps the returned items client-side (default 30; 0 = no cap).
	// Applied after pagination / --all-pages accumulation and --since filter.
	Limit int
	// AllPages walks server pages internally, accumulating items until
	// total exhausted or --limit hit.
	AllPages bool
}

// ListService is the narrow SDK surface this command depends on.
type ListService interface {
	GetSessionsByTenant(ctx context.Context, page, pageSize int) ([]sdk.Session, int, error)
}

// listResult is the typed payload emitted under data.
type listResult struct {
	Items []sdk.Session `json:"items"`
}

// NewCmdList builds `weknora session list`.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{PageSize: defaultPageSize}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List chat sessions for the active context",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runList(c.Context(), opts, jopts, cli)
		},
	}
	cmd.Flags().IntVar(&opts.PageSize, "page-size", defaultPageSize, "Items per server batch (1..1000)")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 30, "Maximum results to return (0 = no cap, 1..10000 = explicit)")
	cmd.Flags().BoolVar(&opts.AllPages, "all-pages", false, "Walk all server pages until exhausted (or --limit hit)")
	cmd.Flags().StringVar(&opts.Since, "since", "", "Only show sessions updated within `duration` (e.g. 7d, 24h, 30m)")
	cmdutil.AddJSONFlags(cmd, sessionListFields)
	aiclient.SetAgentHelp(cmd, "Lists chat sessions. data.{items}; pagination metadata in _meta.{page, page_size, total, has_more}. --all-pages drains every server page in one call (capped by --limit). --since filters client-side after fetch.")
	return cmd
}

func runList(ctx context.Context, opts *ListOptions, jopts *cmdutil.JSONOptions, svc ListService) error {
	if opts.PageSize < 1 || opts.PageSize > maxPageSize {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--page-size must be in 1..%d, got %d", maxPageSize, opts.PageSize),
		}
	}
	if opts.Limit < 0 || opts.Limit > 10000 {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--limit must be in 0..10000 (0 = no cap), got %d", opts.Limit),
		}
	}
	var since time.Duration
	if opts.Since != "" {
		d, err := parseSinceDuration(opts.Since)
		if err != nil {
			return err
		}
		since = d
	}

	var (
		items []sdk.Session
		total int
	)
	if opts.AllPages {
		accum := make([]sdk.Session, 0)
		page := 1
		for {
			chunk, t, err := svc.GetSessionsByTenant(ctx, page, opts.PageSize)
			if err != nil {
				return cmdutil.WrapHTTP(err, "list sessions")
			}
			total = t
			accum = append(accum, chunk...)
			if opts.Limit > 0 && len(accum) >= opts.Limit {
				accum = accum[:opts.Limit]
				break
			}
			if page*opts.PageSize >= total || len(chunk) == 0 {
				break
			}
			page++
		}
		items = accum
	} else {
		chunk, t, err := svc.GetSessionsByTenant(ctx, 1, opts.PageSize)
		if err != nil {
			return cmdutil.WrapHTTP(err, "list sessions")
		}
		items = chunk
		total = t
	}
	if items == nil {
		items = []sdk.Session{} // JSON [] not null
	}
	if since > 0 {
		threshold := time.Now().Add(-since)
		filtered := items[:0]
		for _, s := range items {
			t, err := time.Parse(time.RFC3339, s.UpdatedAt)
			if err != nil {
				continue // skip unparseable timestamps rather than guess
			}
			if t.After(threshold) {
				filtered = append(filtered, s)
			}
		}
		items = filtered
	}
	// --limit applies after --since so the cap reflects what the user sees.
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	if jopts.Enabled() {
		// has_more is suppressed when --since filter is active (server
		// total ≠ what we returned) or when --all-pages drained the server.
		meta := &format.Meta{
			Page:     1,
			PageSize: opts.PageSize,
			Total:    int64(total),
		}
		if since == 0 && !opts.AllPages {
			meta.HasMore = opts.PageSize < total
		}
		return format.WriteEnvelopeFiltered(
			iostreams.IO.Out,
			format.Success(listResult{Items: items}, meta),
			jopts.Fields, jopts.JQ,
		)
	}

	if len(items) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no sessions)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tUPDATED")
	now := time.Now()
	for _, s := range items {
		title := text.Truncate(50, s.Title)
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, title, fuzzyTime(now, s.UpdatedAt))
	}
	return tw.Flush()
}

// parseSinceDuration accepts `time.ParseDuration` forms (1h30m, 24h, 30m)
// plus a `<N>d` suffix for whole days (e.g. 7d). Returns an
// input.invalid_argument *cmdutil.Error for unparseable inputs or
// non-positive durations.
func parseSinceDuration(s string) (time.Duration, error) {
	raw := strings.TrimSpace(s)
	var d time.Duration
	var err error
	if rest, ok := strings.CutSuffix(raw, "d"); ok {
		num, perr := strconv.ParseFloat(rest, 64)
		if perr != nil {
			err = perr
		} else {
			d = time.Duration(num * float64(24*time.Hour))
		}
	} else {
		d, err = time.ParseDuration(raw)
	}
	if err != nil {
		return 0, &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--since %q is not a valid duration: %v (try 7d, 24h, 30m, 1h30m)", s, err),
		}
	}
	if d <= 0 {
		return 0, &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("--since must be positive, got %q", s),
		}
	}
	return d, nil
}

// fuzzyTime renders a server-provided timestamp string in "2d ago" form.
// Returns the raw input if parsing fails — better to surface the unknown
// format than to silently render "-".
func fuzzyTime(now time.Time, ts string) string {
	if ts == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return text.FuzzyAgo(now, t)
}
