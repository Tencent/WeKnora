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

// downloadFields enumerates the fields surfaced for `--format json` discovery on
// `doc download`. Matches the downloadResult struct.
var downloadFields = []string{"path", "bytes", "filename"}

// downloadResult is the typed payload emitted as the success envelope when
// --format json is requested and output is going to a file (not stdout).
type downloadResult struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Filename string `json:"filename"`
}

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
		Use:   "download <doc-id>",
		Short: "Download a document by ID",
		Long: `Streams the document bytes to disk (or stdout with --output -).

Default behavior (no --output): writes to the cwd under the filename the
server suggests via Content-Disposition. If the server doesn't suggest
one, the command errors and asks for --output FILE explicitly.

Existing files are NOT overwritten unless --clobber is passed.

With --format json, on success emits a JSON envelope whose data has
path, bytes, and filename fields. When output is stdout (--output -),
the JSON envelope is suppressed because the raw bytes already occupy
stdout.`,
		Example: `  weknora doc download doc_abc                       # writes ./<server-name>
  weknora doc download doc_abc -O report.pdf
  weknora doc download doc_abc --output -            # stream to stdout (binary safe)
  weknora doc download doc_abc -O report.pdf --clobber
  weknora doc download doc_abc -O report.pdf --format json   # JSON envelope`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			cli, err := f.Client()
			if err != nil {
				return err
			}
			return runDownload(c.Context(), opts, fopts, cli, args[0])
		},
	}
	cmd.Flags().StringVarP(&opts.Output, "output", "O", "", `Output path; "-" for stdout. Defaults to the server-suggested filename.`)
	cmd.Flags().BoolVar(&opts.Clobber, "clobber", false, "Overwrite the output file if it already exists")
	cmdutil.AddFormatFlag(cmd, downloadFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Download a document's bytes by id. Writes a file (or stdout with --output -).",
		RequiredFlags: []string{"<doc-id> (positional)"},
		Output:        "with --format json (file output): envelope.data has path, bytes, filename; suppressed with --output - (raw bytes to stdout)",
	})
	return cmd
}

func runDownload(ctx context.Context, opts *DownloadOptions, fopts *cmdutil.FormatOptions, svc DownloadService, id string) error {
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
		// Raw bytes go to stdout — suppress any JSON envelope regardless of
		// --format json because both can't occupy stdout simultaneously.
		_, err := io.Copy(iostreams.IO.Out, body)
		return err
	}
	if err := refuseIfExists(dest, opts.Clobber); err != nil {
		return err
	}
	return streamToFile(body, dest, fopts)
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
//
// On success: if fopts.WantsJSON(), emits a downloadResult envelope to
// stdout instead of the "✓ Saved" text.
func streamToFile(body io.Reader, path string, fopts *cmdutil.FormatOptions) error {
	f, err := os.Create(path)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "create %s", path)
	}
	n, copyErr := io.Copy(f, body)
	if copyErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, copyErr, "write %s", path)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "close %s", path)
	}

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, downloadResult{
			Path:     path,
			Bytes:    n,
			Filename: filepath.Base(path),
		}, nil)
	}
	fmt.Fprintf(iostreams.IO.Err, "✓ Saved %s\n", path)
	return nil
}
