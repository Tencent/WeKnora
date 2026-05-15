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
		&cmdutil.JSONOptions{}, svc, &testutil.ConfirmPrompter{})
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
		&cmdutil.JSONOptions{}, svc, &testutil.ConfirmPrompter{}))
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
		&cmdutil.JSONOptions{}, svc, &testutil.ConfirmPrompter{})
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
		nil, svc, p))
	assert.True(t, p.Asked)
	assert.Equal(t, "c1", svc.gotChunkID)
}

func TestDelete_TTY_ConfirmNo_Aborts(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeChunkDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runDelete(context.Background(),
		&DeleteOptions{ChunkID: "c1", DocID: "doc_abc"},
		nil, svc, p)
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
		&cmdutil.JSONOptions{}, svc, &testutil.ConfirmPrompter{}))
	body := out.String()
	assert.Contains(t, body, `"id":"c1"`)
	assert.Contains(t, body, `"deleted":true`)
}
