package doc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/text"
	sdk "github.com/Tencent/WeKnora/client"
)

// docReparseFields enumerates the fields surfaced for `--format json` discovery
// on `doc reparse`. The result is the Knowledge object; the caller mainly wants
// the id and the reset parse_status.
var docReparseFields = []string{"id", "parse_status", "file_name", "title"}

// ReparseOptions captures `doc reparse` flag state.
type ReparseOptions struct {
	DryRun bool
}

// ReparseService is the narrow SDK surface this command depends on.
type ReparseService interface {
	ReparseKnowledge(ctx context.Context, id string) (*sdk.Knowledge, error)
}

// NewCmdReparse builds `weknora doc reparse <doc-id>`.
func NewCmdReparse(f *cmdutil.Factory) *cobra.Command {
	opts := &ReparseOptions{}
	cmd := &cobra.Command{
		Use:   "reparse <doc-id>",
		Short: "Re-run parsing on a document",
		Long: `Re-trigger server-side parsing of a document — the recovery path when a
document's parse_status is failed, or after its source changed. The document
keeps its id; parsing restarts asynchronously, so follow with
'weknora doc wait <doc-id>' to block until it reaches a terminal status.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "doc.reparse",
				Args:   map[string]any{"doc": args[0]},
			}); handled {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runReparse(c.Context(), opts, fopts, cli, args[0])
		},
	}
	cmdutil.AddIgnoredKBFlag(cmd)
	cmdutil.AddFormatFlag(cmd, docReparseFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "re-run parsing on a document (recovery after parse_status=failed, or a changed source)",
		RequiredFlags: []string{"<doc-id> (positional)"},
		Examples: []string{
			"weknora doc reparse doc_abc",
			"weknora doc reparse doc_abc --format json",
		},
		Output: "envelope.data is the Knowledge object with parse_status reset to the re-processing state; poll `weknora doc wait <doc-id>` for completion",
	})
	return cmd
}

func runReparse(ctx context.Context, opts *ReparseOptions, fopts *cmdutil.FormatOptions, svc ReparseService, id string) error {
	k, err := svc.ReparseKnowledge(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "reparse document %s", id)
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, k, nil)
	}
	// fileName → title, with no id fallback (empty means "skip the suffix").
	name := text.KnowledgeDisplayName(k.FileName, k.Title, "")
	w := iostreams.IO.Out
	fmt.Fprintf(w, "✓ reparse triggered for %s", id)
	if name != "" {
		fmt.Fprintf(w, " (%s)", name)
	}
	fmt.Fprintln(w)
	if k.ParseStatus != "" {
		fmt.Fprintf(w, "  parse_status: %s\n", k.ParseStatus)
	}
	fmt.Fprintf(w, "  next: weknora doc wait %s\n", id)
	return nil
}
