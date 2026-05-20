package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

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
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `{"id":"doc_abc"`), "expected bare object; got %q", got)
	assert.Contains(t, got, `"deleted":true`)
	assert.NotContains(t, got, `"ok":`)
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
// Multi-id tests (runMultiDelete, keep-going semantics)
// ---------------------------------------------------------------------------

func TestRunMultiDelete_AllSucceed(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	res, err := runMultiDelete(
		context.Background(),
		&DeleteOptions{Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
		svc,
		&testutil.ConfirmPrompter{Answer: true},
		[]string{"a", "b", "c"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, res.OK)
	assert.Empty(t, res.Failed)
	assert.Equal(t, 3, svc.calls)
}

func TestRunMultiDelete_KeepGoingOnError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{errFor: map[string]error{"doc_b": errors.New("not found")}}
	res, err := runMultiDelete(
		context.Background(),
		&DeleteOptions{Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
		svc,
		&testutil.ConfirmPrompter{Answer: true},
		[]string{"doc_a", "doc_b", "doc_c"},
	)
	require.Error(t, err, "partial failure must return non-nil error (exit 1)")
	assert.Equal(t, 3, svc.calls, "all ids must be attempted (keep-going)")
	require.Len(t, res.OK, 2)
	require.Len(t, res.Failed, 1)
	assert.Equal(t, "doc_b", res.Failed[0].ID)
	assert.Equal(t, "not found", res.Failed[0].Message)
	// OK list must contain only successful ids
	assert.Contains(t, res.OK, "doc_a")
	assert.Contains(t, res.OK, "doc_c")
}

func TestRunMultiDelete_AllFail(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{errFor: map[string]error{
		"x": errors.New("HTTP error 404: not found"),
		"y": errors.New("HTTP error 403: forbidden"),
	}}
	res, err := runMultiDelete(
		context.Background(),
		&DeleteOptions{Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
		svc,
		&testutil.ConfirmPrompter{Answer: true},
		[]string{"x", "y"},
	)
	require.Error(t, err)
	assert.Empty(t, res.OK)
	assert.Len(t, res.Failed, 2)
}

func TestRunMultiDelete_ConfirmBatch_NonTTY_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t) // non-TTY
	svc := &fakeDeleteSvc{}
	_, err := runMultiDelete(
		context.Background(),
		&DeleteOptions{Yes: false},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON},
		svc,
		&testutil.ConfirmPrompter{Answer: false},
		[]string{"a", "b"},
	)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 0, svc.calls, "must not call DeleteKnowledge without confirmation")
}

func TestRunMultiDelete_ConfirmBatch_TTY_UserAborts(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	_, err := runMultiDelete(
		context.Background(),
		&DeleteOptions{Yes: false},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText},
		svc,
		&testutil.ConfirmPrompter{Answer: false},
		[]string{"a", "b", "c"},
	)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Contains(t, errBuf.String(), "Aborted.")
	assert.Equal(t, 0, svc.calls, "user aborted ⇒ SDK must NOT be called")
}

// ---------------------------------------------------------------------------
// Emit tests
// ---------------------------------------------------------------------------

func TestEmitMultiDelete_JSON(t *testing.T) {
	var buf bytes.Buffer
	res := &MultiDeleteResult{
		OK:     []string{"a", "b"},
		Failed: []FailedItem{{ID: "c", Message: "x"}},
	}
	err := emitMultiDelete(res, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, &buf)
	require.NoError(t, err)

	var got MultiDeleteResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, []string{"a", "b"}, got.OK)
	require.Len(t, got.Failed, 1)
	assert.Equal(t, "c", got.Failed[0].ID)
}

func TestEmitMultiDelete_Text(t *testing.T) {
	var buf bytes.Buffer
	res := &MultiDeleteResult{
		OK:     []string{"a"},
		Failed: []FailedItem{{ID: "b", Message: "boom"}},
	}
	err := emitMultiDelete(res, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, &buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "OK a")
	assert.Contains(t, out, "FAIL b: boom")
}

func TestEmitMultiDelete_TextEmpty(t *testing.T) {
	var buf bytes.Buffer
	res := &MultiDeleteResult{OK: []string{"x", "y"}}
	err := emitMultiDelete(res, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, &buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "OK x")
	assert.Contains(t, out, "OK y")
	assert.NotContains(t, out, "FAIL")
}

func TestEmitMultiDelete_UnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	res := &MultiDeleteResult{}
	err := emitMultiDelete(res, &cmdutil.FormatOptions{Mode: "yaml"}, &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml")
}
