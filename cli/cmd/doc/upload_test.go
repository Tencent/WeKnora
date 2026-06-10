package doc

import (
	"context"
	"encoding/json"
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
	_ *sdk.KnowledgeProcessOverrides,
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

func TestUpload_Success_Text(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	path := writeTempFile(t, "report.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_99", FileName: "report.pdf"}}
	opts := &UploadOptions{}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))

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
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	assert.Equal(t, "Q3 Marketing Report.pdf", svc.got.customName)
}

func TestUpload_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	path := writeTempFile(t, "a.md")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_77", FileName: "a.md"}}
	opts := &UploadOptions{}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_xxx", path))

	got := out.String()
	var env struct {
		OK   bool          `json:"ok"`
		Data sdk.Knowledge `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &env), "expected valid JSON envelope, got %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.Equal(t, "doc_77", env.Data.ID, "envelope.data.id must be doc_77")
	assert.Contains(t, got, `"file_name":"a.md"`)
}

func TestUpload_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "x.txt")
	svc := &fakeUploadSvc{err: errors.New("HTTP error 500: internal")}
	err := runUpload(context.Background(), &UploadOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestUpload_HTTPError_409Conflict(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "dup.pdf")
	svc := &fakeUploadSvc{err: errors.New("HTTP error 409: file exists")}
	err := runUpload(context.Background(), &UploadOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceAlreadyExists, typed.Code)
}

// TestUpload_DuplicateFileMaps_resource_already_exists pins the contract that
// the SDK's sentinel sdk.ErrDuplicateFile (returned with no "HTTP error <n>:"
// prefix because the duplicate is detected by file-hash short-circuit, not by
// status code) is mapped to resource.already_exists.
func TestUpload_DuplicateFileMaps_resource_already_exists(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "dup.md")
	svc := &fakeUploadSvc{err: sdk.ErrDuplicateFile}
	err := runUpload(context.Background(), &UploadOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path)
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

func TestValidateUploadFlags_NoPath_Rejected(t *testing.T) {
	err := validateUploadFlags(&UploadOptions{}, nil)
	require.Error(t, err)
	// Missing required input wraps as FlagError so the exit code (2)
	// matches cobra's MinimumNArgs(1) for commands taking a positional.
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe, "expected FlagError so exit code maps to 2")
	assert.Equal(t, 2, cmdutil.ExitCode(err))
}

func TestValidateUploadFlags_WithPath_OK(t *testing.T) {
	require.NoError(t, validateUploadFlags(&UploadOptions{}, []string{"/tmp/x.pdf"}))
}

// --- C10 expanded flags: multimodel / metadata / channel ---

func TestUpload_EnableMultimodel_Set_True(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "mm.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_mm", FileName: "mm.pdf"}}
	mm := true
	opts := &UploadOptions{EnableMultimodel: &mm}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	require.NotNil(t, svc.got.enableMultimodel, "expected non-nil *bool when flag set")
	assert.True(t, *svc.got.enableMultimodel)
}

func TestUpload_EnableMultimodel_Set_False(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "mm.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_mm", FileName: "mm.pdf"}}
	mm := false
	opts := &UploadOptions{EnableMultimodel: &mm}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	require.NotNil(t, svc.got.enableMultimodel, "explicit false must still surface as non-nil *bool")
	assert.False(t, *svc.got.enableMultimodel)
}

// TestParseTriBool pins the empty-string-rejects behavior. Bare
// --enable-multimodel maps to "true" via NoOptDefVal before the flag reaches
// parseTriBool, so an empty value here always indicates an explicit
// --enable-multimodel="" (e.g. uninterpolated $VAR). Silently coercing
// empty to true used to surprise users.
func TestParseTriBool(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{"true", true, false},
		{"1", true, false},
		{"yes", true, false},
		{"false", false, false},
		{"0", false, false},
		{"no", false, false},
		{"", false, true},   // explicit empty rejected
		{"  ", false, true}, // whitespace rejected
		{"maybe", false, true},
	} {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseTriBool(c.in)
			if c.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "input.invalid_argument")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestUpload_Metadata_ParseKV(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "m.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_m", FileName: "m.pdf"}}
	opts := &UploadOptions{Metadata: []string{"foo=bar", "baz=qux"}}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	assert.Equal(t, map[string]string{"foo": "bar", "baz": "qux"}, svc.got.metadata)
}

func TestUpload_Metadata_EmptyValueAllowed(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "m.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_m", FileName: "m.pdf"}}
	opts := &UploadOptions{Metadata: []string{"foo="}}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	assert.Equal(t, map[string]string{"foo": ""}, svc.got.metadata)
}

func TestUpload_Metadata_LastWins(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "m.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_m", FileName: "m.pdf"}}
	opts := &UploadOptions{Metadata: []string{"k=v1", "k=v2"}}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	assert.Equal(t, map[string]string{"k": "v2"}, svc.got.metadata)
}

func TestUpload_Metadata_InvalidFormat_NoEquals(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "m.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_m", FileName: "m.pdf"}}
	opts := &UploadOptions{Metadata: []string{"foo"}}
	err := runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestUpload_Metadata_InvalidFormat_EmptyKey(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "m.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_m", FileName: "m.pdf"}}
	opts := &UploadOptions{Metadata: []string{"=bar"}}
	err := runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestUpload_Channel_Override(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "c.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_c", FileName: "c.pdf"}}
	opts := &UploadOptions{Channel: "browser_extension"}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	assert.Equal(t, "browser_extension", svc.got.channel)
}

func TestUpload_Channel_DefaultStillAPI(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "c.pdf")
	svc := &fakeUploadSvc{resp: &sdk.Knowledge{ID: "doc_c", FileName: "c.pdf"}}
	// Empty Channel is the runUpload contract for "use default".
	opts := &UploadOptions{}
	require.NoError(t, runUpload(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path))
	assert.Equal(t, uploadChannel, svc.got.channel)
}
