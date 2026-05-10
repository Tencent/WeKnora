// Package linkcmd implements `weknora link` — re-points an existing
// .weknora/project.yaml at a different knowledge base. Distinct from
// `weknora init`: link assumes the directory is already a project (or you
// want one without going through the init wizard) and never prompts.
package linkcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
)

// Options captures `weknora link` flags.
type Options struct {
	KBID    string // --kb-id
	KBName  string // --kb
	JSONOut bool   // --json
}

// linkResult is the typed payload emitted under data. Keep schema-aligned
// with init.initResult so agents see the same shape regardless of entry point.
type linkResult struct {
	Context         string `json:"context"`
	KBID            string `json:"kb_id"`
	KBName          string `json:"kb_name,omitempty"`
	ProjectLinkPath string `json:"project_link_path"`
}

// NewCmd builds the `weknora link` command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Update or create the project link in the current directory",
		Long: `Writes (overwriting if present) .weknora/project.yaml in the current
working directory pointing at the supplied knowledge base. Unlike init, link
never prompts and never fails on an existing file — it is the non-interactive
re-link path used by scripts that switch between knowledge bases.`,
		Example: `  weknora link --kb-id kb_abc
  weknora link --kb staging`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runLink(c.Context(), opts, f)
		},
	}
	cmd.Flags().StringVar(&opts.KBID, "kb-id", "", "Knowledge base id to link")
	cmd.Flags().StringVar(&opts.KBName, "kb", "", "Knowledge base name (resolved to id)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	cmd.MarkFlagsMutuallyExclusive("kb-id", "kb")
	cmd.MarkFlagsOneRequired("kb-id", "kb")
	agent.SetAgentHelp(cmd, "Re-points .weknora/project.yaml to a KB. Pass --kb-id (or --kb name); never prompts, always overwrites.")
	return cmd
}

func runLink(ctx context.Context, opts *Options, f *cmdutil.Factory) error {
	cwd, err := os.Getwd()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "get cwd")
	}
	linkPath := filepath.Join(cwd, projectlink.DirName, projectlink.FileName)

	cfg, err := f.Config()
	if err != nil {
		return err
	}
	ctxName := cfg.CurrentContext
	if ctxName == "" {
		return cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no active context; run `weknora auth login` first")
	}

	kbID, kbName := opts.KBID, ""
	if opts.KBName != "" {
		cli, err := f.Client()
		if err != nil {
			return err
		}
		id, err := cmdutil.ResolveKBNameToID(ctx, cli, opts.KBName)
		if err != nil {
			return err
		}
		kbID, kbName = id, opts.KBName
	}

	link := &projectlink.Project{
		Context:   ctxName,
		KBID:      kbID,
		CreatedAt: time.Now().UTC(),
	}
	if err := projectlink.Save(linkPath, link); err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write project link")
	}

	r := linkResult{
		Context:         ctxName,
		KBID:            kbID,
		KBName:          kbName,
		ProjectLinkPath: linkPath,
	}
	if opts.JSONOut {
		return format.WriteEnvelope(iostreams.IO.Out, format.Success(r, &format.Meta{
			Context: ctxName,
			KBID:    kbID,
		}))
	}
	if kbName != "" {
		fmt.Fprintf(iostreams.IO.Out, "✓ Linked %s to %s (kb=%s, id=%s)\n", linkPath, ctxName, kbName, kbID)
	} else {
		fmt.Fprintf(iostreams.IO.Out, "✓ Linked %s to %s (kb_id=%s)\n", linkPath, ctxName, kbID)
	}
	return nil
}

