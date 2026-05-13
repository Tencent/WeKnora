package doc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// fakeDownloadSvc scripts an OpenKnowledgeFile response. The fake returns
// a ReadCloser over `content` and reports `filename` as the server-
// suggested name.
type fakeDownloadSvc struct {
	content  string
	filename string
	err      error
	gotID    string
}

func (f *fakeDownloadSvc) OpenKnowledgeFile(_ context.Context, id string) (string, io.ReadCloser, error) {
	f.gotID = id
	if f.err != nil {
		return "", nil, f.err
	}
	return f.filename, io.NopCloser(strings.NewReader(f.content)), nil
}

func TestDownload_DefaultUsesServerFilename(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	tmp := t.TempDir()
	prevWD, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmp))
	defer os.Chdir(prevWD)

	svc := &fakeDownloadSvc{content: "PDF-1.4 bytes", filename: "report.pdf"}
	require.NoError(t, runDownload(context.Background(), &DownloadOptions{}, svc, "doc_abc"))

	got, err := os.ReadFile(filepath.Join(tmp, "report.pdf"))
	require.NoError(t, err)
	assert.Equal(t, "PDF-1.4 bytes", string(got))
}

func TestDownload_OutFile(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dest := filepath.Join(t.TempDir(), "out.bin")
	svc := &fakeDownloadSvc{content: "hello", filename: "ignored.txt"}
	require.NoError(t, runDownload(context.Background(), &DownloadOptions{Output: dest}, svc, "doc_abc"))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
}

func TestDownload_OutDash_Stdout(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDownloadSvc{content: "binary payload", filename: "report.pdf"}
	require.NoError(t, runDownload(context.Background(), &DownloadOptions{Output: "-"}, svc, "doc_abc"))
	assert.Equal(t, "binary payload", out.String())
}

func TestDownload_NoFilenameFromServer_DefaultPath_Errors(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDownloadSvc{content: "x", filename: ""}
	err := runDownload(context.Background(), &DownloadOptions{}, svc, "doc_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
	assert.Contains(t, typed.Hint, "--output")
}

func TestDownload_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDownloadSvc{err: errors.New("HTTP error 404: not found")}
	err := runDownload(context.Background(), &DownloadOptions{Output: filepath.Join(t.TempDir(), "x")}, svc, "doc_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDownload_RefusesOverwrite(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dest := filepath.Join(t.TempDir(), "exists.bin")
	require.NoError(t, os.WriteFile(dest, []byte("OLD"), 0o644))

	svc := &fakeDownloadSvc{content: "NEW", filename: ""}
	err := runDownload(context.Background(), &DownloadOptions{Output: dest}, svc, "doc_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	got, _ := os.ReadFile(dest)
	assert.Equal(t, "OLD", string(got), "must not overwrite without --clobber")
}

// TestDownload_RejectsServerPathTraversal proves that a malicious or buggy
// server cannot escape the cwd via Content-Disposition: only the basename
// of the suggested filename is accepted.
func TestDownload_RejectsServerPathTraversal(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	tmp := t.TempDir()
	prevWD, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmp))
	defer os.Chdir(prevWD)

	// The server sends "../../etc/shadow" — we accept only "shadow" and
	// write to cwd.
	svc := &fakeDownloadSvc{content: "exfil", filename: "../../etc/shadow"}
	require.NoError(t, runDownload(context.Background(), &DownloadOptions{}, svc, "doc_abc"))

	// File must land inside cwd; parent dirs untouched.
	got, err := os.ReadFile(filepath.Join(tmp, "shadow"))
	require.NoError(t, err)
	assert.Equal(t, "exfil", string(got))
}

// TestDownload_RejectsBareDotDot covers the literal-".." case: a server
// returning Content-Disposition: attachment; filename=".." would, before
// the rejection list was extended, pass `filepath.Base("..") == ".."`
// through to os.Create and produce a confusing local.file_io wrap.
func TestDownload_RejectsBareDotDot(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	for _, name := range []string{"..", "../"} {
		_, err := resolveDownloadDest(&DownloadOptions{}, name)
		require.Error(t, err, "filename=%q must be rejected", name)
		var typed *cmdutil.Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	}
}

func TestDownload_ForceOverwrites(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dest := filepath.Join(t.TempDir(), "exists.bin")
	require.NoError(t, os.WriteFile(dest, []byte("OLD"), 0o644))

	svc := &fakeDownloadSvc{content: "NEW", filename: ""}
	require.NoError(t, runDownload(context.Background(), &DownloadOptions{Output: dest, Clobber: true}, svc, "doc_abc"))
	got, _ := os.ReadFile(dest)
	assert.Equal(t, "NEW", string(got))
}
