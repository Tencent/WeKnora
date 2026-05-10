package doc

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// uploadChannel is the ingestion-channel tag the server records for CLI uploads.
// Distinct from "web" (browser UI), "browser_extension" (one-click capture),
// and "wechat" (mini-program). The server uses this only for analytics.
const uploadChannel = "api"

// UploadOptions captures `weknora doc upload` flags.
type UploadOptions struct {
	Name    string
	JSONOut bool
	DryRun  bool
}

// UploadService is the narrow SDK surface this command depends on.
// *sdk.Client satisfies it.
type UploadService interface {
	CreateKnowledgeFromFile(
		ctx context.Context,
		kbID, filePath string,
		metadata map[string]string,
		enableMultimodel *bool,
		customFileName, channel string,
	) (*sdk.Knowledge, error)
}

// NewCmdUpload builds `weknora doc upload <file>`.
func NewCmdUpload(f *cmdutil.Factory) *cobra.Command {
	opts := &UploadOptions{}
	cmd := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload a local file to the knowledge base",
		Long: `Uploads a file (PDF / DOCX / Markdown / TXT / etc.) to the resolved
knowledge base. KB resolution follows the standard 4-level chain:
--kb flag > WEKNORA_KB_ID env > .weknora/project.yaml > error. The --kb
flag accepts either a kb_<id> or a name (resolved via list).

Pass --name to override the recorded file name (useful when the local file
has a generic name like "report.pdf" but you want to surface it as e.g.
"Q3 Marketing Report.pdf" in the UI).

v0.2 ships single-file upload only; --recursive / --glob and progress UI are
planned for v0.3.`,
		Example: `  weknora doc upload report.pdf
  weknora doc upload notes.md --kb kb_abc
  weknora doc upload notes.md --kb my-kb
  weknora doc upload q3.pdf --name "Q3 Marketing Report.pdf"`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			path := args[0]
			if err := validateUploadPath(path); err != nil {
				return err
			}
			opts.DryRun = cmdutil.IsDryRun(c)
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			if opts.DryRun {
				return runUpload(c.Context(), opts, nil, kbID, path)
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runUpload(c.Context(), opts, cli, kbID, path)
		},
	}
	cmd.Flags().String("kb", "", "Knowledge base id (kb_…) or name (overrides env / project link)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "Custom file name to record (defaults to base name)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Uploads one local file to the resolved KB. Refuses non-regular files (dir / symlink). Returns data: Knowledge object with id, parse_status. Pass --kb outside a linked project.")
	return cmd
}

// validateUploadPath checks that path exists and refers to a regular file.
// Symlinks and directories are rejected up-front so users get a typed error
// instead of an opaque SDK failure mid-upload. os.Stat (not Lstat) is used
// here so a symlink to a regular file is accepted — that matches what
// `cp` / `git add` do, and the SDK opens the file via os.Open which follows
// symlinks anyway.
func validateUploadPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cmdutil.Wrapf(cmdutil.CodeUploadFileNotFound, err, "file not found: %s", path)
		}
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "stat %s", path)
	}
	if !info.Mode().IsRegular() {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			fmt.Sprintf("not a regular file: %s (directories and devices are not supported)", path))
	}
	return nil
}

func runUpload(ctx context.Context, opts *UploadOptions, svc UploadService, kbID, path string) error {
	if opts.DryRun {
		return cmdutil.EmitDryRun(opts.JSONOut,
			map[string]string{"file": path, "kb_id": kbID, "name": opts.Name},
			&format.Meta{KBID: kbID},
			&format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("upload %s to kb %s", path, kbID)})
	}

	k, err := svc.CreateKnowledgeFromFile(ctx, kbID, path, nil /*metadata*/, nil /*enableMultimodel*/, opts.Name, uploadChannel)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.ClassifyHTTPError(err), err, "upload %s", path)
	}

	if opts.JSONOut {
		risk := &format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("uploaded %s", path)}
		return format.WriteEnvelope(iostreams.IO.Out, format.SuccessWithRisk(k, &format.Meta{KBID: kbID}, risk))
	}
	displayed := opts.Name
	if displayed == "" {
		displayed = k.FileName
	}
	if displayed == "" {
		displayed = path
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ Uploaded %q (id: %s)\n", displayed, k.ID)
	return nil
}
