package doc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeUploadSvc captures call arguments and returns canned responses.
type fakeUploadSvc struct {
	resp *sdk.Knowledge
	err  error
	got  struct {
		kbID, filePath, customName, channel string
		metadata                            map[string]string
		enableMultimodel                    *bool
	}
}

func (f *fakeUploadSvc) CreateKnowledgeFromFile(
	_ context.Context,
	kbID, filePath string,
	metadata map[string]string,
	enableMultimodel *bool,
	customFileName, channel string,
) (*sdk.Knowledge, error) {
	f.got.kbID = kbID
	f.got.filePath = filePath
	f.got.metadata = metadata
	f.got.enableMultimodel = enableMultimodel
	f.got.customName = customFileName
	f.got.channel = channel
	return f.resp, f.err
}

// writeTempFile creates a regular file under t.TempDir() with sample content.
func writeTempFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("hello world"), 0o644))
	return path
}

func TestUpload_Success_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	path := writeTempFile(t, "report.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_99", FileName: "report.pdf"}}
	opts := &UploadOptions{}
	require.NoError(t, runUpload(context.Background(), opts, svc, "kb_xxx", path))

	assert.Equal(t, "kb_xxx", svc.got.kbID)
	assert.Equal(t, path, svc.got.filePath)
	assert.Equal(t, "", svc.got.customName, "no --name ⇒ empty (server uses base name)")
	assert.Equal(t, uploadChannel, svc.got.channel)
	assert.Nil(t, svc.got.metadata)
	assert.Nil(t, svc.got.enableMultimodel)

	got := out.String()
	for _, want := range []string{"✓", "Uploaded", "report.pdf", "doc_99"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}

func TestUpload_Success_CustomName(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "q3.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_88", FileName: "q3.pdf"}}
	opts := &UploadOptions{Name: "Q3 Marketing Report.pdf"}
	require.NoError(t, runUpload(context.Background(), opts, svc, "kb_xxx", path))
	assert.Equal(t, "Q3 Marketing Report.pdf", svc.got.customName)
}

func TestUpload_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	path := writeTempFile(t, "a.md")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_77", FileName: "a.md"}}
	opts := &UploadOptions{JSONOut: true}
	require.NoError(t, runUpload(context.Background(), opts, svc, "kb_xxx", path))

	got := out.String()
	assert.True(t, strings.HasPrefix(got, `{"ok":true`), "envelope should start with ok:true; got %q", got)
	assert.Contains(t, got, `"id":"doc_77"`)
	assert.Contains(t, got, `"file_name":"a.md"`)
	assert.Contains(t, got, `"kb_id":"kb_xxx"`, "_meta.kb_id should carry the resolved kb id")
}

func TestUpload_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "x.txt")
	svc := &fakeUploadSvc{err: errors.New("HTTP error 500: internal")}
	err := runUpload(context.Background(), &UploadOptions{}, svc, "kb_xxx", path)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestUpload_HTTPError_409Conflict(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "dup.pdf")
	svc := &fakeUploadSvc{err: errors.New("HTTP error 409: file exists")}
	err := runUpload(context.Background(), &UploadOptions{}, svc, "kb_xxx", path)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceAlreadyExists, typed.Code)
}

func TestValidateUploadPath_NotFound(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.pdf")
	err := validateUploadPath(missing)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUploadFileNotFound, typed.Code)
}

func TestValidateUploadPath_DirectoryRejected(t *testing.T) {
	dir := t.TempDir() // already exists, is a dir
	err := validateUploadPath(dir)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Message, "not a regular file")
}

func TestValidateUploadPath_RegularFileAccepted(t *testing.T) {
	path := writeTempFile(t, "ok.txt")
	require.NoError(t, validateUploadPath(path))
}

func TestValidateUploadPath_SymlinkToFileAccepted(t *testing.T) {
	target := writeTempFile(t, "target.txt")
	link := filepath.Join(t.TempDir(), "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}
	// os.Stat (not Lstat) should follow the symlink and report regular file.
	require.NoError(t, validateUploadPath(link))
}
