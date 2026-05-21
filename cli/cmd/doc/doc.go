// Package doc implements the `weknora doc` subtree (list / view / upload /
// fetch / create / download / delete / wait). Upload supports --recursive /
// --glob for bulk ingestion from local files. Fetch ingests a remote URL.
// Create adds a knowledge entry from inline text content.
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
	}
	cmd.AddCommand(NewCmdCreate(f))
	cmd.AddCommand(NewCmdDelete(f))
	cmd.AddCommand(NewCmdDownload(f))
	cmd.AddCommand(NewCmdFetch(f))
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdUpload(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdWait(f))
	return cmd
}
