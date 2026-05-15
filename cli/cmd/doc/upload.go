package doc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// uploadChannel is the default ingestion-channel tag the server records for
// CLI uploads. Distinct from "web" (browser UI), "browser_extension"
// (one-click capture), and "wechat" (mini-program). The server uses this only
// for analytics. Users can override via --channel for cross-tool replay.
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

	// EnableMultimodel toggles server-side multimodal extraction
	// (e.g. images-in-PDF → OCR'd text). nil means "server default" -
	// the flag was not set. true/false explicitly override.
	EnableMultimodel *bool

	// Metadata is the raw --metadata key=value list. Parsed into a map
	// at run-time; empty values allowed, duplicate keys last-wins.
	Metadata []string

	// Channel overrides the ingestion-channel tag recorded server-side.
	// Empty ⇒ uploadChannel ("api"). Free-form: server validates.
	Channel string

	// URL-mode only fields. RunE-side validation rejects these if
	// --from-url is not set (positional file path or --recursive).
	Title    string // --title: display title (URL mode)
	FileType string // --file-type: extension hint for extension-less URLs
	TagID    string // --tag-id: associate the new knowledge entry with a tag
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

The three input modes (positional file / --recursive directory walk /
--from-url remote ingest) are mutually exclusive - pass exactly one.
Use --recursive --glob to upload a directory tree (see Examples).

Server-side ingestion knobs:

  --enable-multimodel      Toggle multimodal extraction (image-in-PDF → text).
                           Unset ⇒ server default; pass true or false to override.
                           Applies to file / --recursive / --from-url.
  --metadata key=value     Attach arbitrary key/value metadata. Repeatable.
                           Empty value allowed; duplicate keys ⇒ last-wins.
                           Malformed values (no '=', empty key) ⇒
                           input.invalid_argument. File and --recursive modes
                           only; rejected on --from-url because the URL-ingest
                           request type carries no metadata field.
  --channel <name>         Override the ingestion-channel tag (default "api").
                           Applies to file / --recursive / --from-url.

URL mode (--from-url) additionally accepts --title, --file-type, and --tag-id.
Passing any of those without --from-url is rejected as input.invalid_argument.`,
		Example: `  weknora doc upload report.pdf
  weknora doc upload notes.md --kb a32a63ff-fb36-4874-bcaa-30f48570a694
  weknora doc upload notes.md --kb my-kb
  weknora doc upload q3.pdf --name "Q3 Marketing Report.pdf"
  weknora doc upload report.pdf --enable-multimodel --metadata team=alpha --metadata sprint=Q4
  weknora doc upload ./docs --recursive --glob '*.pdf' --metadata team=alpha
  weknora doc upload --from-url https://example.com/whitepaper.pdf
  weknora doc upload --from-url https://example.com/no-ext --file-type pdf --title "Whitepaper"
  weknora doc upload --from-url https://example.com/article.html --name "Q3 Article" --tag-id tag_abc`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			jopts, err := cmdutil.CheckJSONFlags(c)
			if err != nil {
				return err
			}
			// Translate the tri-state --enable-multimodel flag into the
			// *bool the SDK expects. Cobra's BoolVar can't distinguish
			// "unset" from "false", so we register a String flag and read
			// Changed() + the raw value here.
			if c.Flags().Changed("enable-multimodel") {
				raw, _ := c.Flags().GetString("enable-multimodel")
				v, perr := parseTriBool(raw)
				if perr != nil {
					return perr
				}
				opts.EnableMultimodel = &v
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
	// Tri-state flag: unset ⇒ server default, "true"/"false" override. The
	// raw string is decoded into opts.EnableMultimodel in RunE.
	cmd.Flags().String("enable-multimodel", "", "Toggle multimodal extraction (true|false); unset ⇒ server default")
	cmd.Flags().Lookup("enable-multimodel").NoOptDefVal = "true"
	cmd.Flags().StringSliceVar(&opts.Metadata, "metadata", nil, "Attach metadata `key=value` (repeatable; empty value allowed, last-wins on duplicate keys)")
	cmd.Flags().StringVar(&opts.Channel, "channel", "", "Ingestion-channel tag recorded server-side (default \"api\")")
	cmd.Flags().StringVar(&opts.Title, "title", "", "Display title for the new entry (--from-url only)")
	cmd.Flags().StringVar(&opts.FileType, "file-type", "", "File-type hint such as \"pdf\" when the URL has no extension (--from-url only)")
	cmd.Flags().StringVar(&opts.TagID, "tag-id", "", "Tag id to associate with the new entry (--from-url only)")
	cmdutil.AddJSONFlags(cmd, docUploadFields)
	return cmd
}

// parseTriBool parses the raw --enable-multimodel string into a bool. Bare
// --enable-multimodel (no value) is treated as "true" via NoOptDefVal at
// registration time; callers gate on Changed() so an unset flag never gets
// here. An explicit empty string (e.g. --enable-multimodel="" from an
// uninterpolated shell variable) is rejected as input.invalid_argument
// rather than silently coerced.
func parseTriBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			fmt.Sprintf("--enable-multimodel expects true|false, got %q", raw))
	}
}

// parseMetadataKV converts the raw --metadata key=value slice into a map.
// Empty values are allowed. Duplicate keys ⇒ last-wins. Returns nil when
// the slice is empty so callers pass nil through to the SDK unchanged.
func parseMetadataKV(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, kv := range raw {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			return nil, cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				fmt.Sprintf("--metadata expects key=value (got %q)", kv))
		}
		out[kv[:eq]] = kv[eq+1:]
	}
	return out, nil
}

// effectiveChannel returns the channel string to send to the SDK. Empty
// opts.Channel falls back to the default "api" so the wire payload is
// identical to the pre-flag behavior.
func effectiveChannel(opts *UploadOptions) string {
	if opts.Channel != "" {
		return opts.Channel
	}
	return uploadChannel
}

// validateUploadFlags enforces mutual exclusion between the three input
// modes (positional file path / --recursive directory walk / --from-url
// remote ingest) and validates the URL when --from-url is set. It also
// rejects the URL-mode-only flags (--title, --file-type, --tag-id) when
// --from-url isn't set so misuse fails fast with a typed code.
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
		// The server's URL-ingest request type has no Metadata field; a
		// --metadata pair would be silently dropped on the wire. Reject
		// up-front so callers don't think they've set metadata when they
		// haven't.
		if len(opts.Metadata) > 0 {
			return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
				"--metadata is not supported with --from-url (server URL-ingest has no metadata field)")
		}
		return cmdutil.ValidateHTTPURL("--from-url", opts.FromURL)
	}
	if !hasPath {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"a file path is required (or pass --from-url)")
	}
	return rejectURLOnlyFlags(opts)
}

// rejectURLOnlyFlags errors on --title / --file-type / --tag-id when
// --from-url is NOT set. Shared between validateUploadFlags (file mode)
// and runUploadRecursive (directory walk) so a future URL-mode-only flag
// only needs to add one entry here instead of two parallel checks.
func rejectURLOnlyFlags(opts *UploadOptions) error {
	if opts.Title != "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"--title is only valid with --from-url")
	}
	if opts.FileType != "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"--file-type is only valid with --from-url")
	}
	if opts.TagID != "" {
		return cmdutil.NewError(cmdutil.CodeInputInvalidArgument,
			"--tag-id is only valid with --from-url")
	}
	return nil
}

// runUploadFromURL ingests a remote URL via SDK CreateKnowledgeFromURL.
// `--name` becomes the FileName hint so the server's "known file extension"
// detection upgrades crawl-mode to file-download-mode when appropriate.
// Server-side knobs (--enable-multimodel, --metadata via Title/TagID/FileType)
// propagate when set; the SDK request struct omits empty fields via
// `json:",omitempty"` tags so wire payload stays minimal.
func runUploadFromURL(ctx context.Context, opts *UploadOptions, jopts *cmdutil.JSONOptions, svc UploadService, kbID string) error {
	req := sdk.CreateKnowledgeFromURLRequest{
		URL:              opts.FromURL,
		FileName:         opts.Name,
		FileType:         opts.FileType,
		EnableMultimodel: opts.EnableMultimodel,
		Title:            opts.Title,
		TagID:            opts.TagID,
		Channel:          effectiveChannel(opts),
	}
	k, err := svc.CreateKnowledgeFromURL(ctx, kbID, req)
	if err != nil {
		if errors.Is(err, sdk.ErrDuplicateURL) {
			// Server returns 409 with the existing knowledge entry's data.
			// Surface as resource.already_exists; the data payload (if any)
			// is observable via err's wrap chain - but the typed code is
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
// here so a symlink to a regular file is accepted - that matches what
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
	meta, err := parseMetadataKV(opts.Metadata)
	if err != nil {
		return err
	}
	k, err := svc.CreateKnowledgeFromFile(ctx, kbID, path, meta, opts.EnableMultimodel, opts.Name, effectiveChannel(opts))
	if err != nil {
		if errors.Is(err, sdk.ErrDuplicateFile) {
			// SDK returns sentinel without an "HTTP error <status>:" prefix
			// (the duplicate is detected by file hash, not by status code),
			// so WrapHTTP would misclassify it as network.error.
			return cmdutil.Wrapf(cmdutil.CodeResourceAlreadyExists, err,
				"file already uploaded to this knowledge base")
		}
		return cmdutil.WrapHTTP(err, "upload %s", path)
	}
	return renderUploadSuccess(k, jopts, "Uploaded", opts.Name, path)
}
