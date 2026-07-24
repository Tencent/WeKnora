// Package skillscmd holds the `weknora skills` command tree: list the bundled
// Agent Skills and install (write) them to an agent's skills directory. The
// skill markdown is embedded in the binary (see cli/skills), so install works
// without a source checkout — replacing the manual symlink-from-checkout MVP.
//
// Package name `skillscmd` avoids colliding with the embed package `skills`.
package skillscmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
	skillsfs "github.com/Tencent/WeKnora/cli/skills"
)

// NewCmd builds the `weknora skills` parent command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List and install the bundled Agent Skills",
	}
	cmd.AddCommand(newCmdList())
	cmd.AddCommand(newCmdInstall())
	return cmd
}

var skillsListFields = []string{"name", "description", "files"}

func newCmdList() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Agent Skills bundled in this binary",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			skills, err := skillsfs.List()
			if err != nil {
				return cmdutil.Wrapf(cmdutil.CodeInternalError, err, "read embedded skills")
			}
			if fopts.WantsJSON() {
				return fopts.Emit(iostreams.IO.Out, skills, &output.Meta{Count: output.IntPtr(len(skills))})
			}
			tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tFILES\tDESCRIPTION")
			for _, s := range skills {
				fmt.Fprintf(tw, "%s\t%d\t%s\n", s.Name, len(s.Files), s.Description)
			}
			return tw.Flush()
		},
	}
	cmdutil.AddFormatFlag(cmd, skillsListFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "list the Agent Skills bundled in this CLI binary (name, description, file count) — discover what `weknora skills install` would write",
		Examples: []string{"weknora skills list --format json"},
		Output:   "envelope.data is an array of {name, description, files[]}",
	})
	return cmd
}

type installResult struct {
	Dir       string   `json:"dir"`
	Installed []string `json:"installed"`
	Skills    []string `json:"skills"`
}

func newCmdInstall() *cobra.Command {
	var dir string
	var force bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the bundled Agent Skills to an agent's skills directory",
		Long: `Write the embedded Agent Skills to disk so an agent (e.g. Claude Code) can
load them. Defaults to ~/.claude/skills; override with --dir for other agents.
Existing files are left untouched unless --force is passed.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())

			target, err := resolveDir(dir)
			if err != nil {
				return err
			}
			skills, err := skillsfs.List()
			if err != nil {
				return cmdutil.Wrapf(cmdutil.CodeInternalError, err, "read embedded skills")
			}

			var planned []string
			names := make([]string, 0, len(skills))
			for _, s := range skills {
				names = append(names, s.Name)
				for _, rel := range s.Files {
					planned = append(planned, filepath.Join(target, rel))
				}
			}

			if handled, err := cmdutil.HandleDryRun(c, dryRun, cmdutil.DryRunPlan{
				Action: "skills.install",
				Args: map[string]any{
					"dir":    target,
					"skills": names,
					"files":  planned,
				},
			}); handled {
				return err
			}

			installed, err := writeSkills(skills, target, force)
			if err != nil {
				return err
			}
			res := installResult{Dir: target, Installed: installed, Skills: names}
			if fopts.WantsJSON() {
				return fopts.Emit(iostreams.IO.Out, res, nil)
			}
			fmt.Fprintf(iostreams.IO.Out, "✓ Installed %d skill(s) to %s\n", len(names), target)
			for _, p := range installed {
				fmt.Fprintf(iostreams.IO.Out, "  %s\n", p)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Target skills directory (default: ~/.claude/skills)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing skill files")
	cmdutil.AddFormatFlag(cmd, "dir", "installed", "skills")
	cmdutil.AddDryRunFlag(cmd, &dryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:  "write the bundled Agent Skills to an agent's skills directory (default ~/.claude/skills) so the agent can load them; --dry-run previews the file list",
		Examples: []string{"weknora skills install --dry-run --format json", "weknora skills install --dir ~/.claude/skills --force"},
		Output:   "envelope.data is {dir, installed:[paths], skills:[names]}",
	})
	return cmd
}

// resolveDir returns the explicit --dir or the default ~/.claude/skills. A
// leading ~ in --dir is expanded to the home directory — otherwise a quoted
// `--dir '~/foo'` (which the shell leaves literal) would create a bogus "~"
// directory tree.
func resolveDir(dir string) (string, error) {
	if dir != "" {
		return expandTilde(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"could not determine home directory; pass --dir explicitly")
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// expandTilde resolves a leading "~" or "~/" path segment to the user's home
// directory. Other forms (including "~user" and a ~ that isn't the first
// segment) are returned unchanged — matching the common shell behavior a CLI
// is expected to reproduce when it receives an unexpanded literal tilde.
func expandTilde(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"could not expand ~ (home directory unknown); pass an absolute --dir")
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[len("~/"):]), nil
}

// writeSkills writes every embedded skill file under target, creating parent
// dirs. Without --force, an existing file is skipped (not overwritten). Returns
// the paths actually written.
func writeSkills(skills []skillsfs.Skill, target string, force bool) ([]string, error) {
	var written []string
	for _, s := range skills {
		for _, rel := range s.Files {
			dst := filepath.Join(target, rel)
			if !force {
				if _, err := os.Stat(dst); err == nil {
					continue // exists; leave it
				}
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return written, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "create dir for %s", dst)
			}
			b, err := skillsfs.ReadFile(rel)
			if err != nil {
				return written, cmdutil.Wrapf(cmdutil.CodeInternalError, err, "read embedded %s", rel)
			}
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return written, cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write %s", dst)
			}
			written = append(written, dst)
		}
	}
	return written, nil
}
