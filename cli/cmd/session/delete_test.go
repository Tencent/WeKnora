package sessioncmd

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

// fakeDeleteSvc records what id was deleted.
type fakeDeleteSvc struct {
	err    error
	gotID  string
	called bool
}

func (f *fakeDeleteSvc) DeleteSession(_ context.Context, id string) error {
	f.called = true
	f.gotID = id
	return f.err
}

func TestDelete_WithYes(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p, "s_abc"))
	assert.True(t, svc.called)
	assert.Equal(t, "s_abc", svc.gotID)
	assert.False(t, p.Asked, "-y must skip prompt")
	assert.Contains(t, out.String(), "Deleted")
}

func TestDelete_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{}, "s_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{}, "s_x")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.False(t, svc.called, "non-TTY without -y must not call DeleteSession")
}

func TestDelete_TTY_ConfirmYes(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p, "s_yes"))
	assert.True(t, p.Asked)
	assert.True(t, svc.called)
}

func TestDelete_TTY_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p, "s_no")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.False(t, svc.called)
	assert.Contains(t, errBuf.String(), "Aborted")
}

// ---------------------------------------------------------------------------
// Multi-id (keep-going semantics)
// ---------------------------------------------------------------------------

// fakeMultiDeleteSvc records every id deleted and can fail-on selected ids.
type fakeMultiDeleteSvc struct {
	deleted []string
	failOn  map[string]error
}

func (f *fakeMultiDeleteSvc) DeleteSession(_ context.Context, id string) error {
	if e, ok := f.failOn[id]; ok {
		return e
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func TestMultiDelete_AllSucceed(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiDeleteSvc{}
	outcomes, err := cmdutil.RunBatch(context.Background(),
		[]string{"s_a", "s_b", "s_c"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteSession(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete session %s", id)
			}
			return nil
		})
	require.NoError(t, err)
	require.Len(t, outcomes, 3)
	for _, oc := range outcomes {
		assert.Nil(t, oc.Err, "expected no error for %s", oc.ID)
	}
	assert.Equal(t, []string{"s_a", "s_b", "s_c"}, svc.deleted)
}

func TestMultiDelete_PartialFailure_KeepsGoing(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiDeleteSvc{failOn: map[string]error{"s_b": errors.New("HTTP error 404: not found")}}
	outcomes, err := cmdutil.RunBatch(context.Background(),
		[]string{"s_a", "s_b", "s_c"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteSession(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete session %s", id)
			}
			return nil
		})
	require.Error(t, err, "any-failed must surface CodeOperationFailed")
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeOperationFailed, typed.Code)
	require.Len(t, outcomes, 3, "all three ids must appear in outcomes (argv order preserved)")
	assert.Nil(t, outcomes[0].Err, "s_a should succeed")
	assert.NotNil(t, outcomes[1].Err, "s_b should fail")
	assert.Nil(t, outcomes[2].Err, "s_c should succeed (keep-going)")
	assert.Equal(t, "s_b", outcomes[1].ID)
}

func TestMultiDelete_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiDeleteSvc{}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	err := cmdutil.ConfirmDestructiveBatch(&testutil.ConfirmPrompter{}, false, fopts.WantsJSON(), "delete", "session", 2, "session.delete", "")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.Empty(t, svc.deleted, "non-TTY without -y must not call DeleteSession")
}

// TestSessionDelete_MultiID_PartialFailure_BatchEnvelope verifies that the JSON
// output for a multi-id partial failure uses the batch envelope shape:
// {ok:false, data:[{id,ok,result?|error?}...], meta:{count,successes,failures}}.
func TestSessionDelete_MultiID_PartialFailure_BatchEnvelope(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeMultiDeleteSvc{failOn: map[string]error{
		"s_b": errors.New("HTTP error 404: not found"),
	}}
	outcomes, runErr := cmdutil.RunBatch(context.Background(),
		[]string{"s_a", "s_b", "s_c"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteSession(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete session %s", id)
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
	assert.Equal(t, "s_a", env.Data[0].ID)
	assert.True(t, env.Data[0].OK)
	assert.Equal(t, "s_b", env.Data[1].ID)
	assert.False(t, env.Data[1].OK)
	assert.NotNil(t, env.Data[1].Error)
	assert.Equal(t, "s_c", env.Data[2].ID)
	assert.True(t, env.Data[2].OK)
}
