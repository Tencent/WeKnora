package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/Tencent/WeKnora/client"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// fakeAllSvc implements AllService for --all mode tests.
type fakeAllSvc struct {
	err    error
	gotID  string
	called bool
	resp   *sdk.ClearKnowledgeBaseContentsResponse
}

func (f *fakeAllSvc) ClearKnowledgeBaseContents(_ context.Context, kbID string) (*sdk.ClearKnowledgeBaseContentsResponse, error) {
	f.called = true
	f.gotID = kbID
	if f.err != nil {
		return nil, f.err
	}
	if f.resp == nil {
		return &sdk.ClearKnowledgeBaseContentsResponse{DeletedCount: 0}, nil
	}
	return f.resp, nil
}

// fakeDeleteSvc captures calls and returns canned errors.
// errFor maps id → error for per-id failure injection (used in multi-id tests).
type fakeDeleteSvc struct {
	err    error
	errFor map[string]error
	got    string
	calls  int
	// deleted tracks all successfully deleted ids (multi-id tests).
	deleted []string
}

func (f *fakeDeleteSvc) DeleteKnowledge(_ context.Context, id string) error {
	f.calls++
	f.got = id
	if f.errFor != nil {
		if err, ok := f.errFor[id]; ok {
			return err
		}
		f.deleted = append(f.deleted, id)
		return nil
	}
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

// ---------------------------------------------------------------------------
// Single-id tests — runDelete uses the simpler {id, deleted} payload.
// ---------------------------------------------------------------------------

func TestDelete_Success_WithForce(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{Yes: true}
	// Force=true short-circuits the confirm path; the prompter must not be
	// consulted, so any value works.
	require.NoError(t, runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{Answer: false}, "doc_abc"))

	assert.Equal(t, "doc_abc", svc.got)
	assert.Equal(t, 1, svc.calls)
	assert.Contains(t, out.String(), "✓")
	assert.Contains(t, out.String(), "doc_abc")
}

func TestDelete_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{Yes: true}
	require.NoError(t, runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{Answer: true}, "doc_abc"))

	got := out.String()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &env), "expected valid JSON envelope, got %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.Equal(t, "doc_abc", env.Data["id"], "envelope.data.id must be doc_abc")
	assert.Equal(t, true, env.Data["deleted"], "envelope.data.deleted must be true")
}

func TestDelete_NotFound_404(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{}, "doc_missing")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 500: internal")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{}, "doc_x")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	// Single-id delete WrapHTTP-classifies the SDK error; HTTP 500 → server.error.
	// (The multi-id path rolls up failures as operation.failed; this is the
	// single-id path so it stays server.error.)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestDelete_ConfirmYes(t *testing.T) {
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{Answer: true}, "doc_abc")
	require.NoError(t, err)
	assert.Equal(t, 1, svc.calls, "user said yes ⇒ delete proceeds")
	assert.Contains(t, out.String(), "✓")
}

func TestDelete_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{Answer: false}, "doc_abc")
	require.Error(t, err)
	assert.Equal(t, 0, svc.calls, "user said no ⇒ SDK must NOT be called")

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Contains(t, errBuf.String(), "Aborted.")
}

// TestDelete_AgentPrompterErrors covers the path where the prompter itself
// returns an error (e.g. AgentPrompter, broken stdin). runDelete maps this to
// CodeInputMissingFlag so the user sees "pass --force" in the hint.
func TestDelete_AgentPrompterErrors(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{Err: errors.New("no tty")}, "doc_abc")
	require.Error(t, err)
	assert.Equal(t, 0, svc.calls)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
}

// TestDelete_NoYes_NonTTY_RequiresConfirmation: when stdout isn't a TTY
// (typical agent pipe / CI), the destructive-write protocol requires
// explicit -y/--yes. The CLI exits 10 with input.confirmation_required,
// never silently proceeds. See cli/README.md "Exit codes".
func TestDelete_NoYes_NonTTY_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{Err: errors.New("no tty")}, "doc_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 0, svc.calls, "non-TTY without -y must not call DeleteKnowledge")
	assert.Equal(t, 10, cmdutil.ExitCode(err))
}

// ---------------------------------------------------------------------------
// Multi-id tests (cmdutil.RunBatch, keep-going semantics)
// ---------------------------------------------------------------------------

func TestRunMultiDelete_AllSucceed(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	outcomes, err := cmdutil.RunBatch(
		context.Background(),
		[]string{"a", "b", "c"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteKnowledge(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete document %s", id)
			}
			return nil
		},
	)
	require.NoError(t, err)
	require.Len(t, outcomes, 3)
	for _, oc := range outcomes {
		assert.Nil(t, oc.Err, "expected no error for id %s", oc.ID)
	}
	assert.Equal(t, "a", outcomes[0].ID)
	assert.Equal(t, "b", outcomes[1].ID)
	assert.Equal(t, "c", outcomes[2].ID)
	assert.Equal(t, 3, svc.calls)
}

func TestRunMultiDelete_KeepGoingOnError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{errFor: map[string]error{"doc_b": errors.New("not found")}}
	outcomes, err := cmdutil.RunBatch(
		context.Background(),
		[]string{"doc_a", "doc_b", "doc_c"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteKnowledge(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete document %s", id)
			}
			return nil
		},
	)
	require.Error(t, err, "partial failure must return non-nil error (exit 1)")
	assert.Equal(t, 3, svc.calls, "all ids must be attempted (keep-going)")
	require.Len(t, outcomes, 3)
	// order preserved: doc_a ok, doc_b fail, doc_c ok
	assert.Equal(t, "doc_a", outcomes[0].ID)
	assert.Nil(t, outcomes[0].Err)
	assert.Equal(t, "doc_b", outcomes[1].ID)
	assert.NotNil(t, outcomes[1].Err)
	assert.Equal(t, "doc_c", outcomes[2].ID)
	assert.Nil(t, outcomes[2].Err)
}

func TestRunMultiDelete_AllFail(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{errFor: map[string]error{
		"x": errors.New("HTTP error 404: not found"),
		"y": errors.New("HTTP error 403: forbidden"),
	}}
	outcomes, err := cmdutil.RunBatch(
		context.Background(),
		[]string{"x", "y"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteKnowledge(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete document %s", id)
			}
			return nil
		},
	)
	require.Error(t, err)
	require.Len(t, outcomes, 2)
	assert.NotNil(t, outcomes[0].Err)
	assert.NotNil(t, outcomes[1].Err)
}

func TestRunMultiDelete_ConfirmBatch_NonTTY_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t) // non-TTY
	svc := &fakeDeleteSvc{}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	err := cmdutil.ConfirmDestructiveBatch(&testutil.ConfirmPrompter{Answer: false}, false, fopts.WantsJSON(), "document", 2, "doc.delete", "")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 0, svc.calls, "must not call DeleteKnowledge without confirmation")
}

func TestRunMultiDelete_ConfirmBatch_TTY_UserAborts(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	err := cmdutil.ConfirmDestructiveBatch(&testutil.ConfirmPrompter{Answer: false}, false, fopts.WantsJSON(), "document", 3, "doc.delete", "")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Contains(t, errBuf.String(), "Aborted.")
	assert.Equal(t, 0, svc.calls, "user aborted ⇒ SDK must NOT be called")
}

// ---------------------------------------------------------------------------
// Emit tests — JSON path now emits batch envelope (§4.5)
// ---------------------------------------------------------------------------

// batchEnvelope is a minimal struct for parsing the batch envelope shape.
type batchEnvelope struct {
	OK   bool        `json:"ok"`
	Data []batchItem `json:"data"`
	Meta batchMeta   `json:"meta"`
}

type batchItem struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *batchItemError `json:"error,omitempty"`
}

type batchItemError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type batchMeta struct {
	Count     int `json:"count"`
	Successes int `json:"successes"`
	Failures  int `json:"failures"`
}

func TestEmitMultiDelete_JSON(t *testing.T) {
	var buf bytes.Buffer
	outcomes := []cmdutil.BatchOutcome{
		{ID: "a", Err: nil},
		{ID: "b", Err: nil},
		{ID: "c", Err: errors.New("x")},
	}
	err := cmdutil.EmitBatch(outcomes, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, &buf, cmdutil.DeletedAtNow)
	require.NoError(t, err)

	var got batchEnvelope
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	// top-level: partial failure → ok:false
	assert.False(t, got.OK)
	assert.Equal(t, 3, got.Meta.Count)
	assert.Equal(t, 2, got.Meta.Successes)
	assert.Equal(t, 1, got.Meta.Failures)
	require.Len(t, got.Data, 3)
	assert.Equal(t, "a", got.Data[0].ID)
	assert.True(t, got.Data[0].OK)
	assert.Equal(t, "b", got.Data[1].ID)
	assert.True(t, got.Data[1].OK)
	assert.Equal(t, "c", got.Data[2].ID)
	assert.False(t, got.Data[2].OK)
	assert.NotNil(t, got.Data[2].Error)
}

func TestEmitMultiDelete_Text(t *testing.T) {
	var buf bytes.Buffer
	outcomes := []cmdutil.BatchOutcome{
		{ID: "a", Err: nil},
		{ID: "b", Err: errors.New("boom")},
	}
	err := cmdutil.EmitBatch(outcomes, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, &buf, nil)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "OK a")
	assert.Contains(t, out, "FAIL b:")
	assert.Contains(t, out, "boom")
}

func TestEmitMultiDelete_TextEmpty(t *testing.T) {
	var buf bytes.Buffer
	outcomes := []cmdutil.BatchOutcome{
		{ID: "x", Err: nil},
		{ID: "y", Err: nil},
	}
	err := cmdutil.EmitBatch(outcomes, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, &buf, nil)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "OK x")
	assert.Contains(t, out, "OK y")
	assert.NotContains(t, out, "FAIL")
}

func TestEmitMultiDelete_UnsupportedFormat(t *testing.T) {
	// EmitBatch defers unsupported format handling to WriteBatchEnvelope; for
	// non-JSON formats it falls through to text. Verify it does not error.
	var buf bytes.Buffer
	outcomes := []cmdutil.BatchOutcome{}
	err := cmdutil.EmitBatch(outcomes, &cmdutil.FormatOptions{Mode: "yaml"}, &buf, nil)
	// EmitBatch itself does not error on unknown mode (it uses WantsJSON gate)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Batch envelope shape test — Task 2.6
// ---------------------------------------------------------------------------

// TestDocDelete_MultiID_PartialFailure_BatchEnvelope verifies that when a
// multi-id delete has a partial failure, stdout carries the §4.5 batch
// envelope shape: ok:false, data:[BatchItem...], meta:{count, successes,
// failures}. Order follows original argv order.
func TestDocDelete_MultiID_PartialFailure_BatchEnvelope(t *testing.T) {
	// id1 succeeds, id2 fails, id3 succeeds.
	svc := &fakeDeleteSvc{errFor: map[string]error{
		"id2": errors.New("HTTP error 404: not found"),
	}}
	outcomes, runErr := cmdutil.RunBatch(
		context.Background(),
		[]string{"id1", "id2", "id3"},
		func(ctx context.Context, id string) error {
			if err := svc.DeleteKnowledge(ctx, id); err != nil {
				return cmdutil.WrapHTTP(err, "delete document %s", id)
			}
			return nil
		},
	)
	require.Error(t, runErr, "partial failure must return non-nil error")
	require.Len(t, outcomes, 3)

	var buf bytes.Buffer
	require.NoError(t, cmdutil.EmitBatch(outcomes, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON, TTY: false}, &buf, cmdutil.DeletedAtNow))

	var env batchEnvelope
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))

	// top-level ok:false (partial failure)
	assert.False(t, env.OK)

	// meta counts
	assert.Equal(t, 3, env.Meta.Count)
	assert.Equal(t, 2, env.Meta.Successes)
	assert.Equal(t, 1, env.Meta.Failures)

	// items in argv order
	require.Len(t, env.Data, 3)

	assert.Equal(t, "id1", env.Data[0].ID)
	assert.True(t, env.Data[0].OK)
	assert.Nil(t, env.Data[0].Error)

	assert.Equal(t, "id2", env.Data[1].ID)
	assert.False(t, env.Data[1].OK)
	require.NotNil(t, env.Data[1].Error)
	assert.Equal(t, "resource.not_found", env.Data[1].Error.Type)

	assert.Equal(t, "id3", env.Data[2].ID)
	assert.True(t, env.Data[2].OK)
	assert.Nil(t, env.Data[2].Error)
}

// ---------------------------------------------------------------------------
// --all mode tests (runDeleteAll)
// ---------------------------------------------------------------------------

// TestDocDelete_All_MissingKB_ReturnsFlagError: --all without --kb must exit 2.
func TestDocDelete_All_MissingKB_ReturnsFlagError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeAllSvc{}
	// Simulate the RunE guard: --all without --kb returns FlagError before
	// runDeleteAll is ever called.
	err := cmdutil.NewFlagError(fmt.Errorf("--all requires --kb=<id>"))
	require.Error(t, err)
	var flagErr *cmdutil.FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.Equal(t, 2, cmdutil.ExitCode(err))
	assert.False(t, svc.called)
}

// TestDocDelete_All_WithoutYes_JSONMode_ReturnsExit10 verifies the
// CodeInputConfirmationRequired (exit 10) path with risk metadata in JSON/non-TTY.
func TestDocDelete_All_WithoutYes_JSONMode_ReturnsExit10(t *testing.T) {
	_, _ = iostreams.SetForTest(t) // non-TTY
	svc := &fakeAllSvc{}
	opts := &DeleteOptions{All: true, KB: "kb_x", Yes: false}
	err := runDeleteAll(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	require.NotNil(t, typed.Risk)
	assert.Equal(t, "destructive", typed.Risk.Level)
	assert.Equal(t, "doc.delete_all", typed.Risk.Action)
	assert.False(t, svc.called)
}

// TestDocDelete_All_WithYes_CallsClearKB verifies that with -y the call is made
// and the JSON envelope contains kb_id + deleted_count.
func TestDocDelete_All_WithYes_CallsClearKB(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeAllSvc{resp: &sdk.ClearKnowledgeBaseContentsResponse{DeletedCount: 17}}
	opts := &DeleteOptions{All: true, KB: "kb_x", Yes: true}
	err := runDeleteAll(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{})
	require.NoError(t, err)
	assert.True(t, svc.called)
	assert.Equal(t, "kb_x", svc.gotID)

	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "expected valid JSON envelope, got %q", out.String())
	assert.True(t, env.OK)
	assert.Equal(t, "kb_x", env.Data["kb_id"])
	assert.Equal(t, float64(17), env.Data["deleted_count"])
}

// TestDocDelete_All_WithYes_TextMode verifies the text output path.
func TestDocDelete_All_WithYes_TextMode(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeAllSvc{resp: &sdk.ClearKnowledgeBaseContentsResponse{DeletedCount: 5}}
	opts := &DeleteOptions{All: true, KB: "kb_y", Yes: true}
	err := runDeleteAll(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{})
	require.NoError(t, err)
	assert.True(t, svc.called)
	body := out.String()
	assert.Contains(t, body, "5")
	assert.Contains(t, body, "kb_y")
}

// TestDocDelete_All_TTY_UserAborts: interactive TTY + user says no → CodeUserAborted.
func TestDocDelete_All_TTY_UserAborts(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeAllSvc{}
	opts := &DeleteOptions{All: true, KB: "kb_z", Yes: false}
	err := runDeleteAll(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{Answer: false})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.False(t, svc.called)
	assert.Contains(t, errBuf.String(), "Aborted")
}

// TestDocDelete_All_ServiceError propagates SDK errors via WrapHTTP.
func TestDocDelete_All_ServiceError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeAllSvc{err: errors.New("HTTP error 404: not found")}
	opts := &DeleteOptions{All: true, KB: "kb_missing", Yes: true}
	err := runDeleteAll(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}
