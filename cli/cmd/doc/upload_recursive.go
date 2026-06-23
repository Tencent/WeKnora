package doc

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/output"
)

// uploadedFile holds the server-side result for a successful per-file upload.
// Keyed by file path in the uploadResults map populated by the RunBatch closure.
type uploadedFile struct {
	ID   string
	Name string
}

// runUploadRecursive walks dir, filters by Glob, and uploads each match
// sequentially. Per-file errors do NOT abort the walk - they accumulate
// and the final return aggregates them so the user sees the full picture
// in one run. Exit semantics: nil error on full success, a typed *cmdutil.Error
// when ≥1 file failed (the typed code mirrors the first failure's
// classification so callers can still branch).
func runUploadRecursive(ctx context.Context, opts *UploadOptions, fopts *cmdutil.FormatOptions, svc UploadService, kbID, dir string) error {
	if opts.Name != "" {
		return &cmdutil.Error{
			Code:    cmdutil.CodeInputInvalidArgument,
			Message: "--name cannot be combined with --recursive (one name can't apply to N files)",
			Hint:    "drop --name or upload files one at a time",
		}
	}
	// Parse --metadata up front so a malformed value aborts before the
	// first SDK call - otherwise a typo in `key=value` would only surface
	// per-file as repeated identical errors.
	meta, err := parseMetadataKV(opts.Metadata)
	if err != nil {
		return err
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
	// explicitly - no in-function default needed.
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
		if fopts.WantsJSON() {
			// Empty batch: emit an all-success envelope with zero items.
			return output.WriteBatchEnvelope(iostreams.IO.Out, nil, fopts.TTY, cmdutil.GetProfile())
		}
		fmt.Fprintf(iostreams.IO.Out, "(no files matched %q under %s)\n", opts.Glob, dir)
		return nil
	}

	// uploaded captures per-path server results for successful uploads.
	// Populated by the RunBatch closure; read by the resultFn below.
	uploaded := make(map[string]uploadedFile, len(matches))
	var firstFailCode cmdutil.ErrorCode
	channel := cmp.Or(opts.Channel, uploadChannel)

	outcomes, runErr := cmdutil.RunBatch(ctx, matches, func(ctx context.Context, p string) error {
		k, err := svc.CreateKnowledgeFromFile(ctx, kbID, p, meta, opts.EnableMultimodel, "", channel, nil)
		if err != nil {
			code := cmdutil.ClassifyHTTPError(err)
			if firstFailCode == "" {
				firstFailCode = code
			}
			// Per-file progress lines are human progress signal; suppress
			// under --format json so they don't precede the JSON object on stdout.
			if !fopts.WantsJSON() {
				fmt.Fprintf(iostreams.IO.Out, "FAIL %s: %v\n", filepath.Base(p), err)
			}
			return err
		}
		id, name := "", ""
		if k != nil {
			id = k.ID
			name = k.FileName
		}
		uploaded[p] = uploadedFile{ID: id, Name: name}
		if !fopts.WantsJSON() {
			fmt.Fprintf(iostreams.IO.Out, "OK   %s (id: %s)\n", filepath.Base(p), id)
		}
		return nil
	})

	failures := 0
	for _, oc := range outcomes {
		if oc.Err != nil {
			failures++
		}
	}

	if fopts.WantsJSON() {
		if err := cmdutil.EmitBatch(outcomes, fopts, iostreams.IO.Out, func(p string) any {
			f := uploaded[p]
			return map[string]any{"id": f.ID, "name": f.Name}
		}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(iostreams.IO.Out, "Uploaded %d, Failed %d\n", len(outcomes)-failures, failures)
	}

	if runErr != nil {
		// Silent on the --format json path: the success object above already
		// carries per-file detail; without Silent the root error handler would
		// print to stderr in addition. ExitCode still walks Code so the typed
		// exit-code-by-class contract holds.
		code := firstFailCode
		if code == "" {
			code = cmdutil.ClassifyContextErr(ctx.Err())
		}
		return &cmdutil.Error{
			Code:    code,
			Message: fmt.Sprintf("%d of %d uploads failed", failures, len(matches)),
			Silent:  fopts.WantsJSON(),
		}
	}
	return nil
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
