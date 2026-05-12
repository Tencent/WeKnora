package sessioncmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type ViewOptions struct {
	JSONOut bool
}

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
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runView(c.Context(), opts, cli, args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Shows a chat session's metadata (title, description, timestamps). Errors with resource.not_found if id is unknown.")
	return cmd
}

func runView(ctx context.Context, opts *ViewOptions, svc ViewService, id string) error {
	s, err := svc.GetSession(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get session %q", id)
	}
	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(s, nil))
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
