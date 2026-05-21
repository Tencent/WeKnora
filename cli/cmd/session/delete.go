package sessioncmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// sessionDeleteFields enumerates the fields surfaced for `--format json`
// discovery on `session delete`. Tracks the single-id result struct;
// multi-id mode emits the batch envelope.
var sessionDeleteFields = []string{"id", "deleted"}

type DeleteOptions struct {
	Yes bool // sourced from the global -y/--yes persistent flag
}

// DeleteService is the narrow SDK surface this command depends on.
type DeleteService interface {
	DeleteSession(ctx context.Context, id string) error
}

// deleteResult is the typed payload emitted under data on single-id success.
type deleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// NewCmdDelete builds `weknora session delete`. Single-id keeps the simpler
// code path; multi-id uses keep-going semantics (one -y confirms all,
// failures collected, exit 1 if any fail).
func NewCmdDelete(f *cmdutil.Factory) *cobra.Command {
	opts := &DeleteOptions{}
	cmd := &cobra.Command{
		Use:   "delete <session-id> [<session-id>...]",
		Short: "Delete one or more chat sessions",
		Long: `Permanently delete one or more chat sessions and their messages.

Prompts for confirmation by default when stdout is a TTY and JSON output is
not set. Pass -y/--yes (global flag) to skip the prompt (required in agent
/ CI / piped contexts).

Single-id: one confirm prompt, exit 0/1.
Multi-id:
  • Default keep-going: failed deletes do NOT stop the run; failures collected.
  • One -y/--yes confirms all sessions.
  • Exit 0 if all succeed; exit 1 if any failed.

AI agents: This is a high-risk write. Without -y/--yes the CLI exits 10
and writes input.confirmation_required to stderr. NEVER auto-pass -y
without the user's explicit go-ahead.`,
		Example: `  weknora session delete s_abc                  # interactive confirm
  weknora session delete s_abc -y               # no prompt
  weknora session delete s_abc -y --format json # bare {id, deleted:true} JSON
  weknora session delete s_a s_b s_c -y         # delete 3, keep-going`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Yes, _ = c.Flags().GetBool("yes")
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			// Single-id uses the simpler code path (bare {id, deleted}).
			if len(args) == 1 {
				return runDelete(c.Context(), opts, fopts, cli, f.Prompter(), args[0])
			}
			if err := cmdutil.ConfirmDestructiveBatch(f.Prompter(), opts.Yes, fopts.WantsJSON(), "session", len(args), "session.delete", "weknora session delete "+strings.Join(args, " ")+" -y"); err != nil {
				return err
			}
			outcomes, runErr := cmdutil.RunBatch(c.Context(), args, func(ctx context.Context, id string) error {
				if err := cli.DeleteSession(ctx, id); err != nil {
					return cmdutil.WrapHTTP(err, "delete session %s", id)
				}
				return nil
			})
			// Only emit when the operation actually ran. Pre-flight errors
			// (e.g. confirmation_required) must leave stdout empty per the
			// wire contract in README.md.
			if len(outcomes) > 0 {
				if emitErr := cmdutil.EmitBatch(outcomes, fopts, iostreams.IO.Out, cmdutil.DeletedAtNow); emitErr != nil {
					return emitErr
				}
			}
			return runErr
		},
	}
	cmdutil.AddFormatFlag(cmd, sessionDeleteFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "permanently delete one or more chat sessions and their messages",
		RequiredFlags: []string{"<session-id>... (positional, at least one)"},
		Examples: []string{
			"weknora session delete s_abc -y",
			"weknora session delete s_a s_b s_c -y",
			"weknora session delete s_abc -y --format json",
		},
		Warnings: []string{
			"session delete drops chat history for that session permanently. Never auto-add -y.",
		},
	})
	return cmd
}

func runDelete(ctx context.Context, opts *DeleteOptions, fopts *cmdutil.FormatOptions, svc DeleteService, p prompt.Prompter, id string) error {
	if err := cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), "session", id, "session.delete", "weknora session delete "+id+" -y"); err != nil {
		return err
	}

	if err := svc.DeleteSession(ctx, id); err != nil {
		return cmdutil.WrapHTTP(err, "delete session %s", id)
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, deleteResult{ID: id, Deleted: true}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Deleted session %s\n", id)
	return nil
}
