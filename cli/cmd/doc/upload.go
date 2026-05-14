package doc

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/aiclient"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// uploadChannel is the ingestion-channel tag the server records for CLI uploads.
// Distinct from "web" (browser UI), "browser_extension" (one-click capture),
// and "wechat" (mini-program). The server uses this only for analytics.
const uploadChannel = "api"

// docUploadFields enumerates the fields surfaced for `--json` discovery on
// `doc upload`. The single-file upload result is the full Knowledge struct;
// these are its top-level json tags.
var docUploadFields = []string{
	"id", "knowledge_base_id", "tag_id", "type", "title", "description",
	"source", "channel", "parse_status", "summary_status", "enable_status",
	"embedding_model_id", "file_name", "file_type", "file_size", "file_hash",
	"file_path", "storage_size",
	"created_at", "updated_at", "processed_at", "error_message",
}

type UploadOptions struct {
	Name      string
	Recursive bool   // --recursive: positional arg is a directory; walk + upload each match
	Glob      string // --glob: filename pattern under --recursive (default "*")
	FromURL   string // --from-url: ingest a remote URL via SDK CreateKnowledgeFromURL
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
	CreateKnowledgeFromURL(
		ctx context.Context,
		kbID string,
		req sdk.CreateKnowledgeFromURLRequest,
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
flag accepts either a KB UUID (passed through) or a name (resolved via list).

Pass --name to override the recorded file name (useful when the local file
has a generic name like "report.pdf" but you want to surface it as e.g.
"Q3 Marketing Report.pdf" in the UI).

Use --recursive --glob to upload a directory tree (see Examples).`,
		Example: `  weknora doc upload report.pdf
  weknora doc upload notes.md --kb a32a63ff-fb36-4874-bcaa-30f48570a694
  weknora doc upload notes.md --kb my-kb
  weknora doc upload q3.pdf --name "Q3 Marketing Report.pdf"
  weknora doc upload ./docs --recursive --glob '*.pdf'
  weknora doc upload --from-url https://example.com/whitepaper.pdf
  weknora doc upload --from-url https://example.com/article.html --name "Q3 Article"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			if err := validateUploadFlags(opts, args); err != nil {
				return err
			}
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}

			switch {
			case opts.FromURL != "":
				return runUploadFromURL(c.Context(), opts, jopts, cli, kbID)
			case opts.Recursive:
				return runUploadRecursive(c.Context(), opts, jopts, cli, kbID, args[0])
			default:
				if err := validateUploadPath(args[0]); err != nil {
					return err
				}
				return runUpload(c.Context(), opts, jopts, cli, kbID, args[0])
			}
		},
	}
	cmd.Flags().String("kb", "", "Knowledge base UUID or name (overrides env / project link)")
	cmd.Flags().StringVar(&opts.Name, "name", "", "Custom file name to record (defaults to base name)")
	cmd.Flags().BoolVar(&opts.Recursive, "recursive", false, "Treat the positional argument as a directory to walk")
	cmd.Flags().StringVar(&opts.Glob, "glob", "*", "Filename pattern to filter when --recursive (e.g. '*.pdf')")
	cmd.Flags().StringVar(&opts.FromURL, "from-url", "", "Ingest a remote `URL` (HTTP/HTTPS) instead of a local file")
	cmdutil.AddJSONFlags(cmd, docUploadFields)
	aiclient.SetAgentHelp(cmd, "Three modes (mutually exclusive): local file (positional), --recursive directory walk + --glob, or --from-url remote ingest. Returns data: Knowledge object (file/URL) or aggregate report (recursive). Errors: resource.already_exists (URL previously ingested) / local.upload_file_not_found.")
	return cmd
}

// validateUploadFlags enforces mutual exclusion between the three input
// modes (positional file path / --recursive directory walk / --from-url
// remote ingest) and validates the URL when --from-url is set.
func validateUploadFlags(opts *UploadOptions, args []string) error {
	hasPath := len(args) == 1
	hasURL := opts.FromURL != ""
	if hasURL {
		if hasPath {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				"cannot pass a file path with --from-url; choose one input mode")
		}
		if opts.Recursive {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				"--recursive cannot be combined with --from-url")
		}
		return cmdutil.ValidateHTTPURL("--from-url", opts.FromURL)
	}
	if !hasPath {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"a file path is required (or pass --from-url)")
	}
	return nil
}

// runUploadFromURL ingests a remote URL via SDK CreateKnowledgeFromURL.
// `--name` becomes the FileName hint so the server's "known file extension"
// detection upgrades crawl-mode to file-download-mode when appropriate.
func runUploadFromURL(ctx context.Context, opts *UploadOptions, jopts *cmdutil.JSONOptions, svc UploadService, kbID string) error {
	req := sdk.CreateKnowledgeFromURLRequest{
		URL:      opts.FromURL,
		FileName: opts.Name,
		Channel:  uploadChannel,
	}
	k, err := svc.CreateKnowledgeFromURL(ctx, kbID, req)
	if err != nil {
		if errors.Is(err, sdk.ErrDuplicateURL) {
			// Server returns 409 with the existing knowledge entry's data.
			// Surface as resource.already_exists; the data payload (if any)
			// is observable via err's wrap chain — but the typed code is
			// what agents branch on.
			return cmdutil.Wrapf(cmdutil.CodeResourceAlreadyExists, err,
				"URL already ingested into this knowledge base")
		}
		return cmdutil.WrapHTTP(err, "ingest URL %s", opts.FromURL)
	}

	return renderUploadSuccess(k, jopts, "Ingested", opts.Name, opts.FromURL)
}

// renderUploadSuccess emits the post-upload result. JSON path is the bare
// Knowledge object; human path prints a checkmark line. Shared by single-
// file upload and URL ingest; humanVerb varies (uploaded/ingested) and
// fallbackDisplay covers the case when the server-recorded file_name is
// blank (URL ingest pre-redirect).
func renderUploadSuccess(k *sdk.Knowledge, jopts *cmdutil.JSONOptions, humanVerb, customName, fallbackDisplay string) error {
	if jopts.Enabled() {
		return jopts.Emit(iostreams.IO.Out, k)
	}
	displayed := customName
	if displayed == "" {
		displayed = k.FileName
	}
	if displayed == "" {
		displayed = fallbackDisplay
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ %s %q (id: %s)\n", humanVerb, displayed, k.ID)
	return nil
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

func runUpload(ctx context.Context, opts *UploadOptions, jopts *cmdutil.JSONOptions, svc UploadService, kbID, path string) error {
	k, err := svc.CreateKnowledgeFromFile(ctx, kbID, path, nil /*metadata*/, nil /*enableMultimodel*/, opts.Name, uploadChannel)
	if err != nil {
		return cmdutil.WrapHTTP(err, "upload %s", path)
	}
	return renderUploadSuccess(k, jopts, "Uploaded", opts.Name, path)
}
