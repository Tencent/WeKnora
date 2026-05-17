package chunkcmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

type fakeChunkDeleteSvc struct {
	gotDocID, gotChunkID string
	err                  error
}

func (f *fakeChunkDeleteSvc) DeleteChunk(_ context.Context, docID, chunkID string) error {
	f.gotDocID = docID
	f.gotChunkID = chunkID
	return f.err
}

func TestDelete_NonTTY_NoYes_ExitTen(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChunkDeleteSvc{}
	err := runDelete(context.Background(),
		&DeleteOptions{ChunkID: "c1", DocID: "doc_abc", Yes: false},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Empty(t, svc.gotChunkID, "must not call DeleteChunk without confirm")
	assert.Equal(t, 10, cmdutil.ExitCode(err), "exit 10 per destructive-write protocol")
}

func TestDelete_WithYes_PassesBothIDs(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChunkDeleteSvc{}
	require.NoError(t, runDelete(context.Background(),
		&DeleteOptions{ChunkID: "c1", DocID: "doc_abc", Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{}))
	assert.Equal(t, "doc_abc", svc.gotDocID)
	assert.Equal(t, "c1", svc.gotChunkID)
}

func TestDelete_MissingDoc_FlagError(t *testing.T) {
	cmd := NewCmdDelete(nil)
	cmd.SetArgs([]string{"c1"}) // no --doc
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	require.Error(t, cmd.Execute())
}

func TestDelete_404_PropagatesNotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChunkDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(),
		&DeleteOptions{ChunkID: "missing", DocID: "doc_abc", Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_TTY_ConfirmYes_Calls(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeChunkDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	require.NoError(t, runDelete(context.Background(),
		&DeleteOptions{ChunkID: "c1", DocID: "doc_abc"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p))
	assert.True(t, p.Asked)
	assert.Equal(t, "c1", svc.gotChunkID)
}

func TestDelete_TTY_ConfirmNo_Aborts(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeChunkDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runDelete(context.Background(),
		&DeleteOptions{ChunkID: "c1", DocID: "doc_abc"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Empty(t, svc.gotChunkID, "answer=no must not call DeleteChunk")
	assert.Contains(t, errBuf.String(), "Aborted")
}

func TestDelete_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChunkDeleteSvc{}
	require.NoError(t, runDelete(context.Background(),
		&DeleteOptions{ChunkID: "c1", DocID: "doc_abc", Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{}))
	body := out.String()
	assert.Contains(t, body, `"id":"c1"`)
	assert.Contains(t, body, `"deleted":true`)
}

// ---------------------------------------------------------------------------
// Multi-id (all chunks share --doc, keep-going on failure)
// ---------------------------------------------------------------------------

// fakeMultiChunkDeleteSvc records (docID, chunkID) pairs and can fail-on
// selected chunkIDs.
type fakeMultiChunkDeleteSvc struct {
	deleted []string // chunk ids successfully deleted
	docIDs  []string // doc id observed for each call
	failOn  map[string]error
}

func (f *fakeMultiChunkDeleteSvc) DeleteChunk(_ context.Context, docID, chunkID string) error {
	f.docIDs = append(f.docIDs, docID)
	if e, ok := f.failOn[chunkID]; ok {
		return e
	}
	f.deleted = append(f.deleted, chunkID)
	return nil
}

func TestMultiDelete_AllSucceed_SharedDoc(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiChunkDeleteSvc{}
	res, err := runMultiDelete(context.Background(),
		&DeleteOptions{DocID: "doc_xyz", Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{},
		[]string{"c1", "c2", "c3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"c1", "c2", "c3"}, res.OK)
	assert.Empty(t, res.Failed)
	// All calls observed the same --doc.
	for _, d := range svc.docIDs {
		assert.Equal(t, "doc_xyz", d)
	}
}

func TestMultiDelete_PartialFailure_KeepsGoing(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiChunkDeleteSvc{failOn: map[string]error{"c2": errors.New("HTTP error 404: not found")}}
	res, err := runMultiDelete(context.Background(),
		&DeleteOptions{DocID: "doc_xyz", Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{},
		[]string{"c1", "c2", "c3"})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeOperationFailed, typed.Code)
	assert.Equal(t, []string{"c1", "c3"}, res.OK, "keep-going: c3 still attempted after c2 failed")
	assert.Len(t, res.Failed, 1)
	assert.Equal(t, "c2", res.Failed[0].ID)
}

func TestMultiDelete_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiChunkDeleteSvc{}
	res, err := runMultiDelete(context.Background(),
		&DeleteOptions{DocID: "doc_xyz"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{},
		[]string{"c1", "c2"})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.Empty(t, res.OK)
	assert.Empty(t, svc.deleted)
}
