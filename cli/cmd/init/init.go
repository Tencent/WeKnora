// Package initcmd implements `weknora init` — creates a per-project link file
// at <cwd>/.weknora/project.yaml that anchors the working tree to a context
// + knowledge-base id (spec §2.4 "weknora init").
//
// Package name is `initcmd` (not `init`) to avoid colliding with Go's reserved
// init() function naming. The cobra Use: string is "init" — what users type.
package initcmd

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

// Options captures `weknora init` flags.
type Options struct {
	Context string // --context: override active context for this link
	KBID    string // --kb-id
	KBName  string // --kb
	Yes     bool   // --yes: skip interactive prompt
	Force   bool   // --force: overwrite existing link
	JSONOut bool   // --json
}

// initResult is the typed payload emitted under data.
type initResult struct {
	Context         string `json:"context"`
	KBID            string `json:"kb_id"`
	KBName          string `json:"kb_name,omitempty"`
	ProjectLinkPath string `json:"project_link_path"`
}

// NewCmd builds the `weknora init` command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Link the current directory to a context + knowledge base",
		Long: `Writes a .weknora/project.yaml in the current working directory that pins
the active context and a knowledge-base id. Subsequent commands run from this
directory (or any subdirectory) automatically resolve --kb-id from the link
unless overridden by the --kb-id / --kb flags or WEKNORA_KB_ID env var.

Mirrors the npm init / cargo init / git init UX pattern: one-time setup that
removes the need to re-pass --kb-id on every command.

AI agents: ` + "`init`" + ` writes to the user's working directory. Only run
it when the user explicitly asked to link this directory — don't run it as a
side effect of unrelated automation.`,
		Example: `  weknora init --kb-id kb_abc                # explicit id
  weknora init --kb engineering --yes        # name → id, no prompt
  weknora init                               # interactive (TTY)
  weknora init --force --kb-id kb_xyz        # overwrite existing link`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runInit(c.Context(), opts, f)
		},
	}
	cmd.Flags().StringVar(&opts.Context, "context", "", "Context to record in the link (defaults to active context)")
	cmd.Flags().StringVar(&opts.KBID, "kb-id", "", "Knowledge base id to link")
	cmd.Flags().StringVar(&opts.KBName, "kb", "", "Knowledge base name (resolved to id)")
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "Skip interactive prompt")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Overwrite an existing .weknora/project.yaml")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	cmd.MarkFlagsMutuallyExclusive("kb-id", "kb")
	agent.SetAgentHelp(cmd, "Creates .weknora/project.yaml linking cwd to a context + KB. Pass --kb-id (or --kb name) and --yes for non-interactive use; --force overwrites an existing link.")
	return cmd
}

func runInit(ctx context.Context, opts *Options, f *cmdutil.Factory) error {
	cwd, err := os.Getwd()
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "get cwd")
	}
	linkPath := filepath.Join(cwd, projectlink.DirName, projectlink.FileName)

	// Pre-flight: refuse to clobber unless --force.
	if _, statErr := os.Stat(linkPath); statErr == nil && !opts.Force {
		return cmdutil.NewError(cmdutil.CodeProjectAlreadyLinked, fmt.Sprintf("project already linked: %s", linkPath))
	}

	// Resolve context: --context flag wins, else config.CurrentContext.
	ctxName, err := resolveContext(opts, f)
	if err != nil {
		return err
	}

	// Resolve KB: --kb-id > --kb > interactive prompt.
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

	r := initResult{
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

// resolveContext returns the context name to record in the link.
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

// resolveKB applies the --kb-id / --kb / prompt fallback chain. Returns
// (kbID, kbName) where kbName is empty when only --kb-id was supplied.
func resolveKB(ctx context.Context, opts *Options, f *cmdutil.Factory) (string, string, error) {
	if opts.KBID != "" {
		return opts.KBID, "", nil
	}
	if opts.KBName != "" {
		cli, err := f.Client()
		if err != nil {
			return "", "", err
		}
		id, err := cmdutil.ResolveKBNameToID(ctx, cli, opts.KBName)
		if err != nil {
			return "", "", err
		}
		return id, opts.KBName, nil
	}
	// No flag: try interactive prompt when allowed.
	if opts.Yes || !iostreams.IO.IsStdoutTTY() {
		return "", "", cmdutil.NewError(cmdutil.CodeKBIDRequired, "--kb-id or --kb is required (no TTY / --yes set)")
	}
	cli, err := f.Client()
	if err != nil {
		return "", "", err
	}
	return promptForKB(ctx, cli, f)
}


// promptForKB lists available knowledge bases on stderr, then asks the user
// for an id or name (huh-backed Input via TTYPrompter). Resolved against the
// listed set so a typed name is converted to the canonical id.
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
