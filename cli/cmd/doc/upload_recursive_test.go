package doc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// scriptedUploadSvc records every CreateKnowledgeFromFile call and returns
// per-path scripted results.
type scriptedUploadSvc struct {
	results map[string]struct {
		k   *sdk.Knowledge
		err error
	}
	called []string
}

func (s *scriptedUploadSvc) CreateKnowledgeFromFile(
	_ context.Context,
	_, filePath string,
	_ map[string]string,
	_ *bool,
	_, _ string,
) (*sdk.Knowledge, error) {
	s.called = append(s.called, filepath.Base(filePath))
	r, ok := s.results[filepath.Base(filePath)]
	if !ok {
		return &sdk.Knowledge{ID: "doc_" + filepath.Base(filePath), FileName: filepath.Base(filePath)}, nil
	}
	return r.k, r.err
}

func mkTree(t *testing.T, base string, names ...string) {
	t.Helper()
	for _, n := range names {
		full := filepath.Join(base, n)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
	}
}

func TestUploadRecursive_WalksAllFiles(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "a.pdf", "b.pdf", "sub/c.pdf")

	svc := &scriptedUploadSvc{}
	opts := &UploadOptions{Recursive: true, Glob: "*"}
	require.NoError(t, runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir))

	sort.Strings(svc.called)
	assert.Equal(t, []string{"a.pdf", "b.pdf", "c.pdf"}, svc.called)
	got := out.String()
	for _, w := range []string{"a.pdf", "b.pdf", "c.pdf", "Uploaded 3"} {
		assert.Contains(t, got, w)
	}
}

func TestUploadRecursive_GlobFilter(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "doc.pdf", "ignore.txt", "sub/keep.pdf", "sub/also-ignore.md")

	svc := &scriptedUploadSvc{}
	opts := &UploadOptions{Recursive: true, Glob: "*.pdf"}
	require.NoError(t, runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir))

	sort.Strings(svc.called)
	assert.Equal(t, []string{"doc.pdf", "keep.pdf"}, svc.called)
}

func TestUploadRecursive_PartialFailure_Exits1(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "ok.pdf", "bad.pdf")

	svc := &scriptedUploadSvc{results: map[string]struct {
		k   *sdk.Knowledge
		err error
	}{
		"bad.pdf": {err: errors.New("HTTP error 500: internal")},
	}}
	opts := &UploadOptions{Recursive: true, Glob: "*"}
	err := runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	// CodeServerError preserves the 500 classification of the underlying
	// SDK error — the recursive wrapper just aggregates.
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)

	got := out.String()
	assert.Contains(t, got, "OK") // ok.pdf still succeeded
	assert.Contains(t, got, "FAIL")
	assert.Contains(t, got, "Uploaded 1")
	assert.Contains(t, got, "Failed 1")
}

func TestUploadRecursive_NoMatches(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "only.txt")

	svc := &scriptedUploadSvc{}
	opts := &UploadOptions{Recursive: true, Glob: "*.pdf"}
	require.NoError(t, runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir))
	assert.Len(t, svc.called, 0)
	assert.Contains(t, strings.ToLower(out.String()), "no files matched")
}

func TestUploadRecursive_NotADirectory(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "single.pdf")
	svc := &scriptedUploadSvc{}
	err := runUploadRecursive(context.Background(), &UploadOptions{Recursive: true, Glob: "*"}, svc, "kb_xxx", path)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Message, "directory")
}

func TestUploadRecursive_RejectsNameFlag(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "a.pdf")
	svc := &scriptedUploadSvc{}
	opts := &UploadOptions{Recursive: true, Glob: "*", Name: "single-name.pdf"}
	err := runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Message, "--name")
}

func TestUploadRecursive_JSON_Envelope(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "ok.pdf", "bad.pdf")

	svc := &scriptedUploadSvc{results: map[string]struct {
		k   *sdk.Knowledge
		err error
	}{
		"bad.pdf": {err: errors.New("HTTP error 500: internal")},
	}}
	opts := &UploadOptions{Recursive: true, Glob: "*", JSONOut: true}
	err := runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir)
	require.Error(t, err) // partial failure → typed error

	body := out.String()
	assert.Contains(t, body, `"kb_id":"kb_xxx"`)
	assert.Contains(t, body, `"uploaded":`)
	assert.Contains(t, body, `"failed":`)
	assert.Contains(t, body, `ok.pdf`)
	assert.Contains(t, body, `bad.pdf`)

	// --json must emit exactly ONE envelope. Per-file "FAIL"/"OK" progress
	// lines belong on the human path; the typed error is Silent so the root
	// handler doesn't write a second Failure envelope on top of ours.
	assert.NotContains(t, body, "FAIL ", "per-file plain lines must not appear under --json")
	assert.NotContains(t, body, "OK   ", "per-file plain lines must not appear under --json")
	assert.Equal(t, 1, strings.Count(body, `"ok":`), "exactly one envelope on stdout")

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.True(t, typed.Silent, "JSON-path partial failure must be Silent")
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestUploadRecursive_DryRun(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "a.pdf", "b.pdf")
	svc := &scriptedUploadSvc{}
	opts := &UploadOptions{Recursive: true, Glob: "*", DryRun: true}
	require.NoError(t, runUploadRecursive(context.Background(), opts, svc, "kb_xxx", dir))
	assert.Len(t, svc.called, 0, "dry-run must not call SDK")
	got := out.String()
	assert.Contains(t, got, "would upload 2")
}
