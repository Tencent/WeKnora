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
	resp    *sdk.Knowledge
	err     error
	urlResp *sdk.Knowledge
	urlErr  error
	got     struct {
		kbID, filePath, customName, channel string
		metadata                            map[string]string
		enableMultimodel                    *bool
		urlReq                              sdk.CreateKnowledgeFromURLRequest
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

func (f *fakeUploadSvc) CreateKnowledgeFromURL(
	_ context.Context,
	kbID string,
	req sdk.CreateKnowledgeFromURLRequest,
) (*sdk.Knowledge, error) {
	f.got.kbID = kbID
	f.got.urlReq = req
	return f.urlResp, f.urlErr
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
	require.NoError(t, runUpload(context.Background(), opts, nil, svc, "kb_xxx", path))

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
	require.NoError(t, runUpload(context.Background(), opts, nil, svc, "kb_xxx", path))
	assert.Equal(t, "Q3 Marketing Report.pdf", svc.got.customName)
}

func TestUpload_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	path := writeTempFile(t, "a.md")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_77", FileName: "a.md"}}
	opts := &UploadOptions{}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx", path))

	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `{"id":"doc_77"`), "expected bare Knowledge object; got %q", got)
	assert.Contains(t, got, `"file_name":"a.md"`)
	assert.NotContains(t, got, `"ok":`)
}

func TestUpload_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "x.txt")
	svc := &fakeUploadSvc{err: errors.New("HTTP error 500: internal")}
	err := runUpload(context.Background(), &UploadOptions{}, nil, svc, "kb_xxx", path)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestUpload_HTTPError_409Conflict(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "dup.pdf")
	svc := &fakeUploadSvc{err: errors.New("HTTP error 409: file exists")}
	err := runUpload(context.Background(), &UploadOptions{}, nil, svc, "kb_xxx", path)
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

// --from-url tests (4-N1).

func TestUploadFromURL_Success_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeUploadSvc{urlResp: &sdk.Knowledge{ID: "doc_url_1", FileName: "whitepaper.pdf"}}
	opts := &UploadOptions{FromURL: "https://example.com/whitepaper.pdf"}
	require.NoError(t, runUploadFromURL(context.Background(), opts, nil, svc, "kb_xxx"))

	assert.Equal(t, "kb_xxx", svc.got.kbID)
	assert.Equal(t, "https://example.com/whitepaper.pdf", svc.got.urlReq.URL)
	assert.Equal(t, "api", svc.got.urlReq.Channel)
	assert.Contains(t, out.String(), "Ingested")
	assert.Contains(t, out.String(), "doc_url_1")
}

func TestUploadFromURL_WithName_Passes_AsFileName(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUploadSvc{urlResp: &sdk.Knowledge{ID: "doc_url_2"}}
	opts := &UploadOptions{FromURL: "https://example.com/article.html", Name: "Q3 Article"}
	require.NoError(t, runUploadFromURL(context.Background(), opts, nil, svc, "kb_xxx"))
	assert.Equal(t, "Q3 Article", svc.got.urlReq.FileName,
		"--name must be forwarded as FileName (server uses it for file-vs-crawl mode hint)")
}

func TestUploadFromURL_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeUploadSvc{urlResp: &sdk.Knowledge{ID: "doc_url_3", FileName: "ok.pdf"}}
	jopts := &cmdutil.JSONOptions{}
	require.NoError(t, runUploadFromURL(context.Background(),
		&UploadOptions{FromURL: "https://example.com/ok.pdf"}, jopts, svc, "kb_xxx"))
	got := out.String()
	assert.Contains(t, got, `"id":"doc_url_3"`)
	assert.NotContains(t, got, `"ok":`)
	assert.NotContains(t, got, `"risk":`)
}

func TestUploadFromURL_DuplicateURLMaps_resource_already_exists(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeUploadSvc{
		urlResp: &sdk.Knowledge{ID: "doc_existing"},
		urlErr:  sdk.ErrDuplicateURL,
	}
	err := runUploadFromURL(context.Background(),
		&UploadOptions{FromURL: "https://example.com/dup.pdf"}, nil, svc, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceAlreadyExists, typed.Code)
}

func TestValidateUploadFlags_FromURL_OK(t *testing.T) {
	require.NoError(t, validateUploadFlags(&UploadOptions{FromURL: "https://example.com/x.pdf"}, nil))
}

func TestValidateUploadFlags_FromURL_WithPositional_Rejected(t *testing.T) {
	err := validateUploadFlags(&UploadOptions{FromURL: "https://example.com/x.pdf"}, []string{"/tmp/x.pdf"})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestValidateUploadFlags_FromURL_WithRecursive_Rejected(t *testing.T) {
	err := validateUploadFlags(&UploadOptions{FromURL: "https://example.com/x.pdf", Recursive: true}, nil)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestValidateUploadFlags_FromURL_BadScheme(t *testing.T) {
	err := validateUploadFlags(&UploadOptions{FromURL: "file:///etc/passwd"}, nil)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestValidateUploadFlags_FromURL_NoHost(t *testing.T) {
	err := validateUploadFlags(&UploadOptions{FromURL: "https://"}, nil)
	require.Error(t, err)
}

func TestValidateUploadFlags_NoPathOrURL_Rejected(t *testing.T) {
	err := validateUploadFlags(&UploadOptions{}, nil)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}
