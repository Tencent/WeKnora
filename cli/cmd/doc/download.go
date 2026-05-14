package doc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

type DownloadOptions struct {
	Output  string // --output / -O: target path, "-" for stdout, "" for server-suggested filename
	Clobber bool   // --clobber: allow overwrite of an existing file
}

// DownloadService is the narrow SDK surface this command depends on. The
// CLI calls OpenKnowledgeFile so it can inspect the server-suggested
// filename and refuse-to-overwrite *before* streaming any bytes.
type DownloadService interface {
	OpenKnowledgeFile(ctx context.Context, knowledgeID string) (string, io.ReadCloser, error)
}

// NewCmdDownload builds `weknora doc download <id>`. Positional id, output
// flag, `-` sentinel for stdout. Flags: `-O, --output <file>` for
// destination, `--clobber` for overwrite control.
func NewCmdDownload(f *cmdutil.Factory) *cobra.Command {
	opts := &DownloadOptions{}
	cmd := &cobra.Command{
		Use:   "download <id>",
		Short: "Download a document by ID",
		Long: `Streams the document bytes to disk (or stdout with --output -).

Default behavior (no --output): writes to the cwd under the filename the
server suggests via Content-Disposition. If the server doesn't suggest
one, the command errors and asks for --output FILE explicitly.

Existing files are NOT overwritten unless --clobber is passed.`,
		Example: `  weknora doc download doc_abc                       # writes ./<server-name>
  weknora doc download doc_abc -O report.pdf
  weknora doc download doc_abc --output -            # stream to stdout (binary safe)
  weknora doc download doc_abc -O report.pdf --clobber`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDownload(c.Context(), opts, cli, args[0])
		},
	}
	cmd.Flags().StringVarP(&opts.Output, "output", "O", "", `Output path; "-" for stdout. Defaults to the server-suggested filename.`)
	cmd.Flags().BoolVar(&opts.Clobber, "clobber", false, "Overwrite the output file if it already exists")
	return cmd
}

func runDownload(ctx context.Context, opts *DownloadOptions, svc DownloadService, id string) error {
	suggested, body, err := svc.OpenKnowledgeFile(ctx, id)
	if err != nil {
		return cmdutil.WrapHTTP(err, "download %s", id)
	}
	defer body.Close()

	dest, err := resolveDownloadDest(opts, suggested)
	if err != nil {
		return err
	}
	if dest == "-" {
		_, err := io.Copy(iostreams.IO.Out, body)
		return err
	}
	if err := refuseIfExists(dest, opts.Clobber); err != nil {
		return err
	}
	return streamToFile(body, dest)
}

// resolveDownloadDest returns the final destination ("-" for stdout, an
// absolute or relative path otherwise) after applying the --output flag
// and sanitizing the server-suggested name. A server that returns a path-
// like filename (..\, /etc/foo) is rejected - only the basename is
// accepted.
func resolveDownloadDest(opts *DownloadOptions, suggested string) (string, error) {
	if opts.Output == "-" {
		return "-", nil
	}
	if opts.Output != "" {
		return opts.Output, nil
	}
	if suggested == "" {
		return "", &cmdutil.Error{
			Code:    cmdutil.CodeInputMissingFlag,
			Message: "server did not supply a filename and --output is unset",
			Hint:    "pass --output FILE (or --output - for stdout)",
		}
	}
	base := filepath.Base(suggested)
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return "", &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("server returned an unusable filename %q", suggested),
			Hint:    "pass --output FILE explicitly",
		}
	}
	return base, nil
}

// refuseIfExists returns CodeInputInvalidArgument when path is present on
// disk and clobber is false. Missing-file is success.
func refuseIfExists(path string, clobber bool) error {
	if clobber {
		return nil
	}
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "stat %s", path)
	}
	return &cmdutil.Error{
		Code:    cmdutil.CodeInputInvalidArgument,
		Message: fmt.Sprintf("%s already exists", path),
		Hint:    "pass --clobber to overwrite",
	}
}

// streamToFile copies body into a newly-created file at path. On any
// streaming error the partial file is removed so callers don't see a
// truncated artifact at the user-visible path.
func streamToFile(body io.Reader, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "create %s", path)
	}
	if _, err := io.Copy(f, body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "write %s", path)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "close %s", path)
	}
	fmt.Fprintf(iostreams.IO.Err, "✓ Saved %s\n", path)
	return nil
}
