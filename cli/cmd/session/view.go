package sessioncmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// sessionViewFields enumerates the fields surfaced for `--json` discovery on
// `session view`. Mirrors sdk.Session json tags.
var sessionViewFields = []string{
	"id", "tenant_id", "title", "description", "created_at", "updated_at",
}

type ViewOptions struct{}

// ViewService is the narrow SDK surface this command depends on.
type ViewService interface {
	GetSession(ctx context.Context, id string) (*sdk.Session, error)
}

// NewCmdView builds `weknora session view <id>`. The server endpoint
// returns metadata only (title/description/timestamps); message content
// lives under a separate session_messages endpoint that the SDK doesn't
// currently wrap, which is why there's no --full flag.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	opts := &ViewOptions{}
	cmd := &cobra.Command{
		Use:   "view <id>",
		Short: "Show a chat session by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), opts, jopts, cli, args[0])
		},
	}
	cmdutil.AddJSONFlags(cmd, sessionViewFields)
	aiclient.SetAgentHelp(cmd, "Shows a chat session's metadata (title, description, timestamps). Errors with resource.not_found if id is unknown.")
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, jopts *cmdutil.JSONOptions, svc ViewService, id string) error {
	s, err := svc.GetSession(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get session %q", id)
	}
	if jopts.Enabled() {
		return format.WriteEnvelopeFiltered(
			iostreams.IO.Out,
			format.Success(s, nil),
			jopts.Fields, jopts.JQ,
		)
	}
	w := iostreams.IO.Out
	fmt.Fprintf(w, "ID:        %s\n", s.ID)
	if s.Title != "" {
		fmt.Fprintf(w, "TITLE:     %s\n", s.Title)
	}
	if s.Description != "" {
		fmt.Fprintf(w, "DESC:      %s\n", s.Description)
	}
	if t, ok := parseTS(s.CreatedAt); ok {
		fmt.Fprintf(w, "CREATED:   %s\n", t.Format("2006-01-02 15:04:05"))
	} else if s.CreatedAt != "" {
		fmt.Fprintf(w, "CREATED:   %s\n", s.CreatedAt)
	}
	if t, ok := parseTS(s.UpdatedAt); ok {
		fmt.Fprintf(w, "UPDATED:   %s\n", t.Format("2006-01-02 15:04:05"))
	} else if s.UpdatedAt != "" {
		fmt.Fprintf(w, "UPDATED:   %s\n", s.UpdatedAt)
	}
	return nil
}

func parseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
