// Package linkcmd implements `weknora link` — binds the current working
// directory to a knowledge base by writing .weknora/project.yaml. Always
// overwrites an existing link silently, mirroring `vercel link` /
// `netlify link` / `kubectl apply` rather than `git init`'s
// refuse-if-exists posture. The cobra Long: text covers the user-facing
// modes (--kb / TTY / non-TTY).
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
	Context string // --context: record a specific context instead of the active one
	KB      string // --kb: kb_<id> or name; empty triggers interactive prompt on TTY
	JSONOut bool   // --json
}

// linkResult is the typed payload emitted under data.
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
		Short: "Bind the current directory to a knowledge base",
		Long: `Writes .weknora/project.yaml in the current working directory pointing
at the supplied knowledge base. Subsequent commands run from this directory
(or any subdirectory) automatically resolve --kb from the link unless
overridden by the --kb flag or WEKNORA_KB_ID env var.

Pass --kb <id-or-name> for non-interactive use (scripts, CI). Run on a TTY
without --kb to be prompted from the list of available KBs. Always overwrites
any existing link — re-run to switch.

AI agents: link writes to the user's working directory. Only run it when the
user explicitly asked to bind this directory; don't run it as a side effect.`,
		Example: `  weknora link --kb kb_abc          # explicit id
  weknora link --kb engineering     # name → id
  weknora link                      # interactive (TTY)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runLink(c.Context(), opts, f)
		},
	}
	cmd.Flags().StringVar(&opts.Context, "context", "", "Context to record in the link (defaults to active context)")
	cmd.Flags().StringVar(&opts.KB, "kb", "", "Knowledge base id (kb_…) or name; omit on a TTY for interactive prompt")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Writes .weknora/project.yaml binding cwd to a KB. Pass --kb (id or name) for non-interactive use. Always overwrites.")
	return cmd
}

func runLink(ctx context.Context, opts *Options, f *cmdutil.Factory) error {
	cwd, err := os.Getwd()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "get cwd")
	}
	linkPath := filepath.Join(cwd, projectlink.DirName, projectlink.FileName)

	ctxName, err := resolveContext(opts, f)
	if err != nil {
		return err
	}

	kbID, kbName, err := resolveKB(ctx, opts, f)
	if err != nil {
		return err
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

// resolveContext picks the auth context to record in the link.
func resolveContext(opts *Options, f *cmdutil.Factory) (string, error) {
	if opts.Context != "" {
		return opts.Context, nil
	}
	cfg, err := f.Config()
	if err != nil {
		return "", err
	}
	if cfg.CurrentContext == "" {
		return "", cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no active context; run `weknora auth login` first")
	}
	return cfg.CurrentContext, nil
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
		return "", "", cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "list knowledge bases")
	}
	if len(kbs) == 0 {
		return "", "", cmdutil.NewError(cmdutil.CodeKBNotFound, "no knowledge bases visible to active context; create one first")
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
