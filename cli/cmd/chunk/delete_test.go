package chunkcmd

import (
	"bytes"
	"context"
	"encoding/json"
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
	docID := "doc_xyz"
	outcomes, err := cmdutil.RunBatch(context.Background(),
		[]string{"c1", "c2", "c3"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteChunk(ctx, docID, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete chunk %s", id)
			}
			return nil
		})
	require.NoError(t, err)
	require.Len(t, outcomes, 3)
	for _, oc := range outcomes {
		assert.Nil(t, oc.Err, "expected no error for %s", oc.ID)
	}
	// All calls observed the same --doc.
	for _, d := range svc.docIDs {
		assert.Equal(t, "doc_xyz", d)
	}
}

func TestMultiDelete_PartialFailure_KeepsGoing(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiChunkDeleteSvc{failOn: map[string]error{"c2": errors.New("HTTP error 404: not found")}}
	docID := "doc_xyz"
	outcomes, err := cmdutil.RunBatch(context.Background(),
		[]string{"c1", "c2", "c3"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteChunk(ctx, docID, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete chunk %s", id)
			}
			return nil
		})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeOperationFailed, typed.Code)
	require.Len(t, outcomes, 3, "all three ids must appear in outcomes (argv order preserved)")
	assert.Nil(t, outcomes[0].Err, "c1 should succeed")
	assert.NotNil(t, outcomes[1].Err, "c2 should fail")
	assert.Nil(t, outcomes[2].Err, "c3 should succeed (keep-going)")
	assert.Equal(t, "c2", outcomes[1].ID)
}

func TestMultiDelete_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiChunkDeleteSvc{}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	err := cmdutil.ConfirmDestructiveBatch(&testutil.ConfirmPrompter{}, false, fopts.WantsJSON(), "chunk", 2, "chunk.delete", "")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.Empty(t, svc.deleted)
}

// TestChunkDelete_MultiID_PartialFailure_BatchEnvelope verifies that the JSON
// output for a multi-id partial failure uses the §4.5 batch envelope shape:
// {ok:false, data:[{id,ok,result?|error?}...], meta:{count,successes,failures}}.
func TestChunkDelete_MultiID_PartialFailure_BatchEnvelope(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiChunkDeleteSvc{failOn: map[string]error{
		"c2": errors.New("HTTP error 404: not found"),
	}}
	docID := "doc_xyz"
	outcomes, runErr := cmdutil.RunBatch(context.Background(),
		[]string{"c1", "c2", "c3"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteChunk(ctx, docID, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete chunk %s", id)
			}
			return nil
		})
	require.Error(t, runErr)
	require.Len(t, outcomes, 3)

	var buf bytes.Buffer
	require.NoError(t, cmdutil.EmitBatch(outcomes, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, &buf, cmdutil.DeletedAtNow))

	var env struct {
		OK   bool `json:"ok"`
		Data []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
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
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env), "envelope must be valid JSON")

	assert.False(t, env.OK, "top-level ok must be false when any item failed")
	assert.Equal(t, 3, env.Meta.Count)
	assert.Equal(t, 2, env.Meta.Successes)
	assert.Equal(t, 1, env.Meta.Failures)
	require.Len(t, env.Data, 3)
	assert.Equal(t, "c1", env.Data[0].ID)
	assert.True(t, env.Data[0].OK)
	assert.Equal(t, "c2", env.Data[1].ID)
	assert.False(t, env.Data[1].OK)
	assert.NotNil(t, env.Data[1].Error)
	assert.Equal(t, "c3", env.Data[2].ID)
	assert.True(t, env.Data[2].OK)
}
