package doc

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// uploadOutcome is one entry in the recursive upload's per-file report.
type uploadOutcome struct {
	Path  string `json:"path"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// runUploadRecursive walks dir, filters by Glob, and uploads each match
// sequentially. Per-file errors do NOT abort the walk — they accumulate
// and the final return aggregates them so the user sees the full picture
// in one run. Exit semantics: nil error on full success, a typed *cmdutil.Error
// when ≥1 file failed (the typed code mirrors the first failure's
// classification so callers can still branch).
func runUploadRecursive(ctx context.Context, opts *UploadOptions, svc UploadService, kbID, dir string) error {
	if opts.Name != "" {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--name cannot be combined with --recursive (one name can't apply to N files)",
			Hint:    "drop --name or upload files one at a time",
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return cmdutil.Wrapf(cmdutil.CodeUploadFileNotFound, err, "directory not found: %s", dir)
		}
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "stat %s", dir)
	}
	if !info.IsDir() {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("not a directory: %s (drop --recursive to upload a single file)", dir),
		}
	}

	// Sanity-check the pattern up front so a typo doesn't show up as "no
	// files matched" per-file. Cobra populates --glob; tests pass it
	// explicitly — no in-function default needed.
	if _, err := filepath.Match(opts.Glob, ""); err != nil {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: fmt.Sprintf("invalid --glob %q: %v", opts.Glob, err),
		}
	}

	matches, err := walkMatches(dir, opts.Glob)
	if err != nil {
		return cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "walk %s", dir)
	}
	if len(matches) == 0 {
		if opts.JSONOut {
			return format.WriteEnvelope(iostreams.IO.Out, format.Success(
				recursiveResult{KBID: kbID}, &format.Meta{KBID: kbID}))
		}
		fmt.Fprintf(iostreams.IO.Out, "(no files matched %q under %s)\n", opts.Glob, dir)
		return nil
	}

	if opts.DryRun {
		previews := make([]uploadOutcome, 0, len(matches))
		for _, m := range matches {
			previews = append(previews, uploadOutcome{Path: m})
		}
		return cmdutil.EmitDryRun(opts.JSONOut,
			recursiveResult{KBID: kbID, Uploaded: previews},
			&format.Meta{KBID: kbID},
			&format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("upload %d file(s) to kb %s", len(matches), kbID)})
	}

	var uploaded, failed []uploadOutcome
	var firstFailCode cmdutil.ErrorCode
	for _, p := range matches {
		k, err := svc.CreateKnowledgeFromFile(ctx, kbID, p, nil, nil, "", uploadChannel)
		if err != nil {
			code := cmdutil.ClassifyHTTPError(err)
			if firstFailCode == "" {
				firstFailCode = code
			}
			failed = append(failed, uploadOutcome{Path: p, Error: err.Error()})
			// Per-file progress lines are human progress signal; suppress
			// under --json so they don't precede the envelope on stdout.
			if !opts.JSONOut {
				fmt.Fprintf(iostreams.IO.Out, "FAIL %s: %v\n", filepath.Base(p), err)
			}
			continue
		}
		id := ""
		if k != nil {
			id = k.ID
		}
		uploaded = append(uploaded, uploadOutcome{Path: p, ID: id})
		if !opts.JSONOut {
			fmt.Fprintf(iostreams.IO.Out, "OK   %s (id: %s)\n", filepath.Base(p), id)
		}
	}

	if opts.JSONOut {
		result := recursiveResult{KBID: kbID, Uploaded: uploaded, Failed: failed}
		risk := &format.Risk{Level: format.RiskWrite, Action: fmt.Sprintf("upload %d file(s) to kb %s", len(matches), kbID)}
		if err := format.WriteEnvelope(iostreams.IO.Out,
			format.SuccessWithRisk(result, &format.Meta{KBID: kbID}, risk)); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(iostreams.IO.Out, "Uploaded %d, Failed %d\n", len(uploaded), len(failed))
	}

	if len(failed) > 0 {
		// Silent on the --json path: the success envelope above already
		// carries per-file uploaded[]/failed[] detail. Without Silent the
		// root error handler would write a second Failure envelope on
		// stdout, corrupting the stream. ExitCode still walks Code so the
		// typed exit-code-by-class contract is preserved.
		return &cmdutil.Error{
			Code:    firstFailCode,
			Message: fmt.Sprintf("%d of %d uploads failed", len(failed), len(matches)),
			Silent:  opts.JSONOut,
		}
	}
	return nil
}

// recursiveResult is the JSON shape emitted under data when --recursive is
// combined with --json. Mirrors the human-mode per-file output: a list of
// successes (Uploaded) and a list of failures (Failed), each with the
// originating path so agents can re-try only the failed entries.
type recursiveResult struct {
	KBID     string          `json:"kb_id"`
	Uploaded []uploadOutcome `json:"uploaded,omitempty"`
	Failed   []uploadOutcome `json:"failed,omitempty"`
}

// walkMatches returns every regular file under root whose base name matches
// pattern. Order is filepath.WalkDir's lexical order (stdlib guarantee on
// every supported FS), which is deterministic for test assertions.
func walkMatches(root, pattern string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip non-regular files (sockets, devices); the SDK can't upload
		// them and they'd show as opaque server errors.
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		ok, merr := filepath.Match(pattern, d.Name())
		if merr != nil {
			return merr
		}
		if ok {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

