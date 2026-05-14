// Package doc implements the `weknora doc` subtree (list / view / upload /
// download / delete). Upload also supports --recursive / --glob for bulk
// ingestion.
//
// "Doc" is the CLI noun; the underlying SDK type is `Knowledge`. The renaming
// is deliberate: end-users think of a knowledge entry as the document they
// uploaded, not as an abstract knowledge unit. Mapping happens in this package
// only - the SDK surface and server API keep the original spelling.
package doc

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora doc` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Manage documents in a knowledge base",
		Args:  cobra.NoArgs,
		Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdUpload(f))
	cmd.AddCommand(NewCmdDownload(f))
	cmd.AddCommand(NewCmdDelete(f))
	return cmd
}
