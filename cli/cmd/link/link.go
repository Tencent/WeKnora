// Package linkcmd implements `weknora link` - binds the current working
// directory to a knowledge base by writing .weknora/project.yaml. Always
// overwrites an existing link silently rather than refusing when one is
// already present. The cobra Long: text covers the user-facing modes
// (--kb / TTY / non-TTY).
package linkcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
)

// linkFields enumerates the fields surfaced for `--format json` discovery on
// `link`. Tracks the small linkResult struct.
var linkFields = []string{"profile", "kb_id", "kb_name", "project_link_path"}

type Options struct {
	KB     string // --kb: KB UUID or name; empty triggers interactive prompt on TTY
	DryRun bool
}

// linkResult is the typed payload emitted under data.
type linkResult struct {
	Profile         string `json:"profile"`
	KBID            string `json:"kb_id"`
	KBName          string `json:"kb_name,omitempty"`
	ProjectLinkPath string `json:"project_link_path"`
}

// NewCmd builds the `weknora link` command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Bind the current directory to a knowledge base",
		Long: `Writes .weknora/project.yaml in the current working directory pointing
at the supplied knowledge base. Subsequent commands run from this directory
(or any subdirectory) automatically resolve --kb from the link unless
overridden by the --kb flag or WEKNORA_KB_ID env var.

Pass --kb <id-or-name> for non-interactive use (scripts, CI). Run on a TTY
without --kb to be prompted from the list of available KBs. Always overwrites
any existing link - re-run to switch.

AI agents: link writes to the user's working directory. Only run it when the
user explicitly asked to bind this directory; don't run it as a side effect.`,
		Example: `  weknora link --kb a32a63ff-fb36-4874-bcaa-30f48570a694    # explicit UUID
  weknora link --kb engineering                             # name → id
  weknora link                                              # interactive (TTY)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Pure-local validation runs before the dry-run gate so --dry-run
			// rejects identically to the live path. resolveProfile only reads
			// config; the non-TTY-without-`--kb` check is a flag-shape error.
			// Same typed errors as runLink (kept there for direct callers).
			if _, err := resolveProfile(f); err != nil {
				return err
			}
			if opts.KB == "" && !iostreams.IO.IsStdoutTTY() {
				return cmdutil.NewError(cmdutil.CodeKBIDRequired, "--kb is required (no TTY for interactive prompt)")
			}
			if handled, err := cmdutil.HandleDryRun(c, opts.DryRun, cmdutil.DryRunPlan{
				Action: "link",
				Args: map[string]any{
					"kb": opts.KB,
				},
			}); handled {
				return err
			}
			return runLink(c.Context(), opts, fopts, f)
		},
	}
	cmd.Flags().StringVar(&opts.KB, "kb", "", "Knowledge base UUID or name; omit on a TTY for interactive prompt")
	cmdutil.AddFormatFlag(cmd, linkFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Bind the current directory to a knowledge base by writing .weknora/project.yaml. Requires --kb (non-interactive); only run when the user explicitly asks to link this directory.",
		RequiredFlags: []string{"--kb (required when no TTY)"},
		Output:        "envelope.data has kb_id, kb_name, project_link_path",
	})
	return cmd
}

func runLink(ctx context.Context, opts *Options, fopts *cmdutil.FormatOptions, f *cmdutil.Factory) error {
	cwd, err := os.Getwd()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "get cwd")
	}
	linkPath := filepath.Join(cwd, projectlink.DirName, projectlink.FileName)

	profileName, err := resolveProfile(f)
	if err != nil {
		return err
	}

	kbID, kbName, err := resolveKB(ctx, opts, f)
	if err != nil {
		return err
	}

	link := &projectlink.Project{
		Profile:   profileName,
		KBID:      kbID,
		CreatedAt: time.Now().UTC(),
	}
	if err := projectlink.Save(linkPath, link); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write project link")
	}

	r := linkResult{
		Profile:         profileName,
		KBID:            kbID,
		KBName:          kbName,
		ProjectLinkPath: linkPath,
	}
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, r, nil)
	}
	if kbName != "" {
		fmt.Fprintf(iostreams.IO.Out, "✓ Linked %s to %s (kb=%s, id=%s)\n", linkPath, profileName, kbName, kbID)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Linked %s to %s (kb_id=%s)\n", linkPath, profileName, kbID)
	}
	return nil
}

// resolveProfile picks the active profile to record in the link. There is no
// per-invocation override flag on `weknora link` itself - to record under a
// different profile, use the global persistent flag (`weknora --profile
// staging link --kb my-kb`); the active profile at link time is what gets
// written.
func resolveProfile(f *cmdutil.Factory) (string, error) {
	cfg, err := f.Config()
	if err != nil {
		return "", err
	}
	if cfg.CurrentProfile == "" {
		return "", cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no active profile; run `weknora auth login` first")
	}
	return cfg.CurrentProfile, nil
}

// resolveKB resolves --kb to (kbID, kbName). Name is empty when the user
// passed an id directly. Falls through to an interactive prompt on a TTY
// when --kb is empty; errors on non-TTY.
func resolveKB(ctx context.Context, opts *Options, f *cmdutil.Factory) (string, string, error) {
	if opts.KB != "" {
		if cmdutil.IsKBID(opts.KB) {
			return opts.KB, "", nil
		}
		cli, err := f.Client()
		if err != nil {
			return "", "", err
		}
		id, err := cmdutil.ResolveKBNameToID(ctx, cli, opts.KB)
		if err != nil {
			return "", "", err
		}
		return id, opts.KB, nil
	}
	if !iostreams.IO.IsStdoutTTY() {
		return "", "", cmdutil.NewError(cmdutil.CodeKBIDRequired, "--kb is required (no TTY for interactive prompt)")
	}
	cli, err := f.Client()
	if err != nil {
		return "", "", err
	}
	return promptForKB(ctx, cli, f)
}

// promptForKB lists available knowledge bases on stderr, then asks the user
// for an id or name. Resolved against the listed set so a typed name is
// converted to the canonical id.
func promptForKB(ctx context.Context, svc cmdutil.KBLister, f *cmdutil.Factory) (string, string, error) {
	kbs, err := svc.ListKnowledgeBases(ctx)
	if err != nil {
		return "", "", cmdutil.WrapHTTP(err, "list knowledge bases")
	}
	if len(kbs) == 0 {
		return "", "", cmdutil.NewError(cmdutil.CodeKBNotFound, "no knowledge bases visible to active profile; create one first")
	}
	fmt.Fprintln(iostreams.IO.Err, "Available knowledge bases:")
	for _, kb := range kbs {
		fmt.Fprintf(iostreams.IO.Err, "  %s  %s\n", kb.ID, kb.Name)
	}
	p := f.Prompter()
	answer, err := p.Input("Knowledge base id or name", "")
	if err != nil {
		return "", "", cmdutil.Wrapf(cmdutil.CodeInputMissingFlag, err, "kb prompt")
	}
	for _, kb := range kbs {
		if kb.ID == answer || kb.Name == answer {
			return kb.ID, kb.Name, nil
		}
	}
	return "", "", cmdutil.NewError(cmdutil.CodeKBNotFound, fmt.Sprintf("knowledge base not found: %s", answer))
}
