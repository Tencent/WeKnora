package doc

import (
	"context"
	"encoding/json"
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

	// Captures from the most-recent call (every recursive iteration writes
	// these; tests that want all-rows can extend to slices later).
	lastMetadata         map[string]string
	lastEnableMultimodel *bool
	lastChannel          string
}

func (s *scriptedUploadSvc) CreateKnowledgeFromFile(
	_ context.Context,
	_, filePath string,
	metadata map[string]string,
	enableMultimodel *bool,
	_, channel string,
	_ *sdk.KnowledgeProcessOverrides,
) (*sdk.Knowledge, error) {
	s.called = append(s.called, filepath.Base(filePath))
	s.lastMetadata = metadata
	s.lastEnableMultimodel = enableMultimodel
	s.lastChannel = channel
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
	require.NoError(t, runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir))

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
	require.NoError(t, runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir))

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
	err := runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	// CodeServerError preserves the 500 classification of the underlying
	// SDK error - the recursive wrapper just aggregates.
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
	require.NoError(t, runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir))
	assert.Len(t, svc.called, 0)
	assert.Contains(t, strings.ToLower(out.String()), "no files matched")
}

func TestUploadRecursive_NotADirectory(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	path := writeTempFile(t, "single.pdf")
	svc := &scriptedUploadSvc{}
	err := runUploadRecursive(context.Background(), &UploadOptions{Recursive: true, Glob: "*"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", path)
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
	err := runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Message, "--name")
}

func TestUploadRecursive_PropagatesMultimodelAndMetadata(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "a.pdf")

	svc := &scriptedUploadSvc{}
	mm := true
	opts := &UploadOptions{
		Recursive:        true,
		Glob:             "*",
		EnableMultimodel: &mm,
		Metadata:         []string{"team=alpha"},
		Channel:          "browser_extension",
	}
	require.NoError(t, runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir))

	require.NotNil(t, svc.lastEnableMultimodel)
	assert.True(t, *svc.lastEnableMultimodel)
	assert.Equal(t, map[string]string{"team": "alpha"}, svc.lastMetadata)
	assert.Equal(t, "browser_extension", svc.lastChannel)
}

func TestUploadRecursive_MetadataInvalid_NoCalls(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dir := t.TempDir()
	mkTree(t, dir, "a.pdf")

	svc := &scriptedUploadSvc{}
	opts := &UploadOptions{Recursive: true, Glob: "*", Metadata: []string{"badformat"}}
	err := runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb_xxx", dir)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Empty(t, svc.called, "must fail before any per-file call")
}

// TestUploadRecursive_JSON_BatchEnvelope verifies that --format json emits the
// batch envelope shape: {ok, data:[{id,ok,result?|error?}...], meta:{count,successes,failures}}.
// The per-item id is the file path; result carries {id, name} from the server.
func TestUploadRecursive_JSON_BatchEnvelope(t *testing.T) {
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
	err := runUploadRecursive(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb_xxx", dir)
	require.Error(t, err) // partial failure → typed error

	var env struct {
		OK   bool `json:"ok"`
		Data []struct {
			ID     string `json:"id"`
			OK     bool   `json:"ok"`
			Result *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"result,omitempty"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		} `json:"data"`
		Meta struct {
			Count     int `json:"count"`
			Successes int `json:"successes"`
			Failures  int `json:"failures"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "must be valid JSON: %s", out.String())

	assert.False(t, env.OK, "top-level ok must be false when any item failed")
	assert.Equal(t, 2, env.Meta.Count)
	assert.Equal(t, 1, env.Meta.Successes)
	assert.Equal(t, 1, env.Meta.Failures)
	require.Len(t, env.Data, 2)

	// File paths are used as batch item ids; verify both files appear.
	ids := []string{env.Data[0].ID, env.Data[1].ID}
	assert.True(t, strings.Contains(ids[0], "ok.pdf") || strings.Contains(ids[1], "ok.pdf"), "ok.pdf must appear in batch data")
	assert.True(t, strings.Contains(ids[0], "bad.pdf") || strings.Contains(ids[1], "bad.pdf"), "bad.pdf must appear in batch data")

	// The success item must have a result with server id/name.
	for _, item := range env.Data {
		if strings.Contains(item.ID, "ok.pdf") {
			assert.True(t, item.OK)
			assert.NotNil(t, item.Result)
		} else {
			assert.False(t, item.OK)
			assert.NotNil(t, item.Error)
		}
	}

	// --format json must emit exactly ONE JSON document. Per-file "FAIL"/"OK"
	// progress lines belong on the human path; the typed error is Silent so
	// the root handler doesn't write anything additional to stdout.
	body := out.String()
	assert.NotContains(t, body, "FAIL ", "per-file plain lines must not appear under --format json")
	assert.NotContains(t, body, "OK   ", "per-file plain lines must not appear under --format json")

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.True(t, typed.Silent, "JSON-path partial failure must be Silent")
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}
