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

const sessionsPageSize = 200

// SessionsSearchOptions captures `weknora search sessions` flag state.
type SessionsSearchOptions struct {
	Query   string
	Limit   int
	JSONOut bool
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
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = strings.TrimSpace(args[0])
			if opts.Query == "" {
				return cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "query argument cannot be empty")
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runSessionsSearch(c.Context(), opts, cli)
		},
	}
	cmd.Flags().IntVarP(&opts.Limit, "limit", "L", 20, "Maximum results to return")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Lists chat sessions whose title or description contains the query. Pages through the tenant sequentially; stops once limit matches found. Returns full Session objects so agents can pivot to session view/delete by id.")
	return cmd
}

func runSessionsSearch(ctx context.Context, opts *SessionsSearchOptions, svc SessionsSearchService) error {
	needle := strings.ToLower(opts.Query)
	var matches []sdk.Session

	for page := 1; ; page++ {
		items, total, err := svc.GetSessionsByTenant(ctx, page, sessionsPageSize)
		if err != nil {
			return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "list sessions")
		}
		for _, s := range items {
			if matchSession(s, needle) {
				matches = append(matches, s)
				if opts.Limit > 0 && len(matches) >= opts.Limit {
					goto done
				}
			}
		}
		if page*sessionsPageSize >= total || len(items) == 0 {
			break
		}
	}
done:
	sortSessionsByRecency(matches)

	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(matches, nil))
	}
	if len(matches) == 0 {
		fmt.Fprintln(iostreams.IO.Out, "(no matches)")
		return nil
	}
	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tUPDATED")
	for _, s := range matches {
		title := text.Truncate(50, s.Title)
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, title, s.UpdatedAt)
	}
	return tw.Flush()
}

// matchSession reports whether title or description contains needle (already
// lowercased by caller).
func matchSession(s sdk.Session, needle string) bool {
	return text.ContainsFold(needle, s.Title, s.Description)
}

// sortSessionsByRecency sorts in place by UpdatedAt desc. Server returns
// strings; we compare lexically — RFC3339 timestamps sort correctly that
// way, and a stable order is enough for output determinism even if a
// non-conforming string slips through.
func sortSessionsByRecency(items []sdk.Session) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
}
