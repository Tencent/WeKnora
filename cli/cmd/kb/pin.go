package kb

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// kbPinFields enumerates the fields surfaced for `--json` discovery on
// `kb pin` / `kb unpin`. The toggle result is the KnowledgeBase; the user-
// relevant fields here are the id and the new pin state.
var kbPinFields = []string{"id", "is_pinned"}

type PinOptions struct{}

// PinService is the narrow SDK surface this command depends on. The CLI
// reads current state before toggling so `pin`/`unpin` are idempotent -
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
		Long:  short + ". Idempotent: reads the current pin state and toggles only if different, so re-running on a KB already in the target state is a no-op.",
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
			return runPin(c.Context(), opts, jopts, cli, args[0], want)
		},
	}
	cmdutil.AddJSONFlags(cmd, kbPinFields)
	return cmd
}

func runPin(ctx context.Context, opts *PinOptions, jopts *cmdutil.JSONOptions, svc PinService, id string, want bool) error {
	verb := "pin"
	if !want {
		verb = "unpin"
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
		// No-op path: the resource is already in the requested state. We
		// emit the current resource so callers see the canonical shape on
		// both fresh-toggle and no-op paths. Human path prints a confirming
		// line; agents observe via the unchanged is_pinned field.
		if jopts.Enabled() {
			return jopts.Emit(iostreams.IO.Out, current)
		}
		fmt.Fprintf(iostreams.IO.Out, "✓ %s is already %s\n", id, state)
		return nil
	}

	updated, err := svc.TogglePinKnowledgeBase(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "%s knowledge base %s", verb, id)
	}
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, updated)
	}
	state := "pinned"
	if !updated.IsPinned {
		state = "unpinned"
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ %s %s\n", id, state)
	return nil
}
