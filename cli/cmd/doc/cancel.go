package doc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func newCmdCancelParse(f *cmdutil.Factory) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "cancel-parse <doc-id>",
		Short:   "Cancel parsing for a pending or processing document, including finalization",
		Example: "  weknora doc cancel-parse doc_abc",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			format, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			format.ResolveDefault(iostreams.IO.IsStdoutTTY())
			if handled, err := cmdutil.HandleDryRun(c, dryRun, cmdutil.DryRunPlan{
				Action: "doc.cancel-parse", Args: map[string]any{"doc": args[0]},
			}); handled {
				return err
			}
			client, err := f.Client()
			if err != nil {
				return err
			}
			doc, err := client.CancelKnowledgeParse(c.Context(), args[0])
			if err != nil {
				return cmdutil.WrapHTTP(err, "cancel parsing for %s", args[0])
			}
			if format.WantsJSON() {
				return format.Emit(iostreams.IO.Out, doc, nil)
			}
			_, err = fmt.Fprintf(iostreams.IO.Out, "%s: %s\n", args[0], doc.ParseStatus)
			return err
		},
	}
	cmdutil.AddIgnoredKBFlag(cmd)
	cmdutil.AddFormatFlag(cmd, "id", "parse_status", "file_name", "title")
	cmdutil.AddDryRunFlag(cmd, &dryRun)
	return cmd
}
