package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type PinOptions struct {
	JSONOut bool
	DryRun  bool
}

// PinService is the narrow SDK surface this command depends on. The CLI
// reads current state before toggling so `pin`/`unpin` are idempotent —
// the server endpoint is only a non-idempotent toggle.
type PinService interface {
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
	TogglePinKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
}

// NewCmdPin builds `weknora kb pin <id>`.
func NewCmdPin(f *cmdutil.Factory) *cobra.Command {
	return newPinCmd(f, "pin", true, "Pin a knowledge base to the top of the list")
}

// NewCmdUnpin builds `weknora kb unpin <id>`.
func NewCmdUnpin(f *cmdutil.Factory) *cobra.Command {
	return newPinCmd(f, "unpin", false, "Unpin a knowledge base")
}

func newPinCmd(f *cmdutil.Factory, use string, want bool, short string) *cobra.Command {
	opts := &PinOptions{}
	cmd := &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.DryRun = cmdutil.IsDryRun(c)
			if opts.DryRun {
				return runPin(c.Context(), opts, nil, args[0], want)
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runPin(c.Context(), opts, cli, args[0], want)
		},
	}
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, fmt.Sprintf("Idempotent %s: reads current pin state, toggles only if different. No-op when already in the requested state.", use))
	return cmd
}

func runPin(ctx context.Context, opts *PinOptions, svc PinService, id string, want bool) error {
	verb := "pin"
	if !want {
		verb = "unpin"
	}
	risk := &format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("%s knowledge base %s", verb, id)}

	if opts.DryRun {
		// Dry-run can't introspect state without a network call by design (see
		// kb/delete.go for the same convention). Report what *would* run if
		// state diverged; agents can disambiguate via a subsequent `kb view`.
		return cmdutil.EmitDryRun(opts.JSONOut, struct {
			ID   string `json:"id"`
			Want bool   `json:"want_pinned"`
		}{id, want}, &format.Meta{KBID: id}, risk)
	}

	current, err := svc.GetKnowledgeBase(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "get knowledge base %s", id)
	}
	if current.IsPinned == want {
		state := "pinned"
		if !want {
			state = "unpinned"
		}
		// No-op path: tell agents what happened. The risk-write classification
		// was the *requested* operation, not what occurred — surface it via a
		// _meta.warning so audit logs don't count a write that wasn't made.
		if opts.JSONOut {
			meta := &format.Meta{KBID: id, Warnings: []string{fmt.Sprintf("already %s — no server call made", state)}}
			return format.WriteEnvelope(iostreams.IO.Out, format.Success(current, meta))
		}
		fmt.Fprintf(iostreams.IO.Out, "✓ %s is already %s\n", id, state)
		return nil
	}

	updated, err := svc.TogglePinKnowledgeBase(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "%s knowledge base %s", verb, id)
	}
	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.SuccessWithRisk(updated, &format.Meta{KBID: id}, risk))
	}
	state := "pinned"
	if !updated.IsPinned {
		state = "unpinned"
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ %s %s\n", id, state)
	return nil
}
