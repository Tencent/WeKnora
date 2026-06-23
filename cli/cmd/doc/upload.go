package doc

import (
	"cmp"
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

// docUploadFields enumerates the fields surfaced for `--format json` discovery on
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

	DryRun bool
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
		processConfig *sdk.KnowledgeProcessOverrides,
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

The two input modes (positional file / --recursive directory walk) are
mutually exclusive - pass exactly one. Use --recursive --glob to upload a
directory tree (see Examples). To ingest a remote URL use "weknora doc fetch";
to create an entry from inline text use "weknora doc create".

Server-side ingestion knobs:

  --enable-multimodel      Toggle multimodal extraction (image-in-PDF → text).
                           Unset ⇒ server default; pass true or false to override.
                           Applies to file / --recursive.
  --metadata key=value     Attach arbitrary key/value metadata. Repeatable.
                           Empty value allowed; duplicate keys ⇒ last-wins.
                           Malformed values (no '=', empty key) ⇒
                           input.invalid_argument.
  --channel <name>         Override the ingestion-channel tag (default "api").
                           Applies to file / --recursive.`,
		Example: `  weknora doc upload report.pdf
  weknora doc upload notes.md --kb a32a63ff-fb36-4874-bcaa-30f48570a694
  weknora doc upload notes.md --kb my-kb
  weknora doc upload q3.pdf --name "Q3 Marketing Report.pdf"
  weknora doc upload report.pdf --enable-multimodel --metadata team=alpha --metadata sprint=Q4
  weknora doc upload ./docs --recursive --glob '*.pdf' --metadata team=alpha`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			// Pure-local validation runs before the dry-run gate so --dry-run
			// rejects identically to the live path. Filesystem stat
			// (validateUploadPath) and recursive directory enumeration are
			// intentionally skipped under --dry-run — those are side-effect-ish
			// reads the preview is allowed to defer.
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
			// Validate --metadata key=value shape upfront; same typed Error as
			// runUpload (kept there for direct-call callers / batch paths).
			if _, err := parseMetadataKV(opts.Metadata); err != nil {
				return err
			}
			if opts.DryRun {
				// Local-only KB resolution: plan reports the raw --kb value
				// without an SDK lookup.
				kbID, err := f.ResolveKBLocal(c)
				if err != nil {
					return err
				}
				filePath := args[0]
				planArgs := map[string]any{
					"file": filePath,
					"kb":   kbID,
				}
				// recursive vs single-file is the blast-radius switch
				// (N docs vs 1); surface it on the plan so agents can
				// decide whether to gate on user approval.
				if opts.Recursive {
					planArgs["recursive"] = true
				}
				if opts.Glob != "" {
					planArgs["glob"] = opts.Glob
				}
				if handled, err := cmdutil.HandleDryRun(c, true, cmdutil.DryRunPlan{
					Action: "doc.upload",
					Args:   planArgs,
				}); handled {
					return err
				}
			}
			cli, err := f.Client()
			if err != nil {
				return err
			}
			kbID, err := f.ResolveKB(c)
			if err != nil {
				return err
			}

			if opts.Recursive {
				return runUploadRecursive(c.Context(), opts, fopts, cli, kbID, args[0])
			}
			if err := validateUploadPath(args[0]); err != nil {
				return err
			}
			return runUpload(c.Context(), opts, fopts, cli, kbID, args[0])
		},
	}
	cmdutil.AddKBFlag(cmd)
	cmd.Flags().StringVar(&opts.Name, "name", "", "Custom file name to record (defaults to base name)")
	cmd.Flags().BoolVar(&opts.Recursive, "recursive", false, "Treat the positional argument as a directory to walk")
	cmd.Flags().StringVar(&opts.Glob, "glob", "*", "Filename pattern to filter when --recursive (e.g. '*.pdf')")
	// Tri-state flag: unset ⇒ server default, "true"/"false" override. The
	// raw string is decoded into opts.EnableMultimodel in RunE.
	cmd.Flags().String("enable-multimodel", "", "Toggle multimodal extraction (true|false); unset ⇒ server default")
	cmd.Flags().Lookup("enable-multimodel").NoOptDefVal = "true"
	cmd.Flags().StringSliceVar(&opts.Metadata, "metadata", nil, "Attach metadata `key=value` (repeatable; empty value allowed, last-wins on duplicate keys)")
	cmd.Flags().StringVar(&opts.Channel, "channel", "", "Ingestion-channel tag recorded server-side (default \"api\")")
	cmdutil.AddFormatFlag(cmd, docUploadFields...)
	cmdutil.AddDryRunFlag(cmd, &opts.DryRun)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor:       "Upload a local file to the resolved knowledge base. KB resolved via --kb flag, WEKNORA_KB_ID env, or project link. Emits the created Knowledge object with its id.",
		RequiredFlags: []string{"<file> (positional)"},
		Output:        "envelope.data is the created Knowledge object with id, knowledge_base_id, file_name, parse_status",
	})
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

// validateUploadFlags enforces mutual exclusion between the two input modes
// (positional file path / --recursive directory walk) and requires that at
// least one input mode is supplied.
func validateUploadFlags(opts *UploadOptions, args []string) error {
	hasPath := len(args) == 1
	if !hasPath {
		// Wrap as FlagError so the exit code (2) matches what cobra's own
		// MinimumNArgs(1) would emit — consistent with every other command
		// that requires a positional argument.
		return cmdutil.NewFlagError(errors.New(
			"a file path is required (or use `weknora doc fetch` for URLs, `weknora doc create` for inline text)"))
	}
	return nil
}

// renderUploadSuccess emits the post-upload result. JSON path is the bare
// Knowledge object; text path prints a checkmark line. Shared by single-
// file upload and URL ingest; verb varies (uploaded/ingested) and
// fallbackDisplay covers the case when the server-recorded file_name is
// blank (URL ingest pre-redirect).
func renderUploadSuccess(k *sdk.Knowledge, fopts *cmdutil.FormatOptions, verb, customName, fallbackDisplay string) error {
	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, k, nil)
	}
	displayed := customName
	if displayed == "" {
		displayed = k.FileName
	}
	if displayed == "" {
		displayed = fallbackDisplay
	}
	fmt.Fprintf(iostreams.IO.Out, "✓ %s %q (id: %s)\n", verb, displayed, k.ID)
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

func runUpload(ctx context.Context, opts *UploadOptions, fopts *cmdutil.FormatOptions, svc UploadService, kbID, path string) error {
	meta, err := parseMetadataKV(opts.Metadata)
	if err != nil {
		return err
	}
	k, err := svc.CreateKnowledgeFromFile(ctx, kbID, path, meta, opts.EnableMultimodel, opts.Name, cmp.Or(opts.Channel, uploadChannel), nil)
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
	return renderUploadSuccess(k, fopts, "Uploaded", opts.Name, path)
}
