package linkcmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
)

// unlinkFields enumerates the fields surfaced for `--json` discovery on
// `unlink`. Tracks the small unlinkResult struct.
var unlinkFields = []string{"project_link_path"}

type UnlinkOptions struct{}

// unlinkResult is the typed payload emitted under data.
type unlinkResult struct {
	ProjectLinkPath string `json:"project_link_path"`
}

// NewCmdUnlink builds `weknora unlink`. Walks up from cwd so a user in
// a subdirectory of a linked project can unlink without cd-ing up first.
func NewCmdUnlink() *cobra.Command {
	opts := &UnlinkOptions{}
	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Remove the directory's knowledge-base binding",
		Long: `Removes the .weknora/project.yaml that ` + "`weknora link`" + ` previously
wrote, so subsequent commands no longer auto-resolve --kb from the link.

Walks up from the current directory until a link is found, mirroring the
discovery that ` + "`--kb`" + ` resolution uses; you do not need to cd to the
project root to unlink. Errors with input.invalid_argument when no link
is present anywhere in the parent chain.`,
		Example: `  weknora unlink           # remove the binding for this project
  weknora unlink --json    # bare JSON (CI / agents)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			return runUnlink(opts, jopts)
		},
	}
	cmdutil.AddJSONFlags(cmd, unlinkFields)
	aiclient.SetAgentHelp(cmd, "Removes .weknora/project.yaml (the cwd → KB binding). Walks up from cwd. Errors with input.invalid_argument when no link is present in the parent chain.")
	return cmd
}

func runUnlink(opts *UnlinkOptions, jopts *cmdutil.JSONOptions) error {
	cwd, err := os.Getwd()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "get cwd")
	}
	linkPath, found, err := projectlink.Discover(cwd)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "discover project link")
	}
	if !found {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("no %s/%s found at or above %s", projectlink.DirName, projectlink.FileName, cwd),
			Hint:    "run `weknora link --kb <id>` first, or check you're in the right directory",
		}
	}
	if err := projectlink.Remove(linkPath); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "remove %s", linkPath)
	}
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, unlinkResult{ProjectLinkPath: linkPath})
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Unlinked %s\n", linkPath)
	return nil
}
