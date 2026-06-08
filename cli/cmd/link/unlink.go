package linkcmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
)

// unlinkFields enumerates the fields surfaced for `--format json` discovery on
// `unlink`. Tracks the small unlinkResult struct.
var unlinkFields = []string{"project_link_path"}

type UnlinkOptions struct {
	DryRun bool
}

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
  weknora unlink --format json    # bare JSON (CI / agents)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Pure-local validation runs before the dry-run gate so --dry-run
			// rejects identically to the live path. projectlink.Discover only
			// reads the filesystem (no SDK / network).
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, cwdErr, "get cwd")
			}
			_, found, discErr := projectlink.Discover(cwd)
			if discErr != nil {
				return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, discErr, "discover project link")
			}
			if !found {
				return &cmdutil.Error{
					Code:    cmdutil.CodeInputInvalidArgument,
					Message: fmt.Sprintf("no %s/%s found at or above %s", projectlink.DirName, projectlink.FileName, cwd),
					Hint:    "run `weknora link --kb <id>` first, or check you're in the right directory",
				}
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "unlink",
				Args:   map[string]any{},
			}); handled {
				return err
			}
			return runUnlink(opts, fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, unlinkFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "Remove the .weknora/project.yaml KB binding from the current directory tree. No flags required; walks up from cwd to find the link.",
		Output:  "envelope.data has project_link_path of the removed file",
	})
	return cmd
}

func runUnlink(opts *UnlinkOptions, fopts *cmdutil.FormatOptions) error {
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
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, unlinkResult{ProjectLinkPath: linkPath}, nil)
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Unlinked %s\n", linkPath)
	return nil
}
