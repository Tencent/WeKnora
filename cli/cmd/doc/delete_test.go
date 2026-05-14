package doc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// fakeDeleteSvc captures the id passed and returns a canned error.
type fakeDeleteSvc struct {
	err   error
	got   string
	calls int
}

func (f *fakeDeleteSvc) DeleteKnowledge(_ context.Context, id string) error {
	f.calls++
	f.got = id
	return f.err
}

func TestDelete_Success_WithForce(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{Yes: true}
	// Force=true short-circuits the confirm path; the prompter must not be
	// consulted, so any value works.
	require.NoError(t, runDelete(context.Background(), opts, nil, svc, &testutil.ConfirmPrompter{Answer: false}, "doc_abc"))

	assert.Equal(t, "doc_abc", svc.got)
	assert.Equal(t, 1, svc.calls)
	assert.Contains(t, out.String(), "✓")
	assert.Contains(t, out.String(), "doc_abc")
}

func TestDelete_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{Yes: true}
	require.NoError(t, runDelete(context.Background(), opts, &cmdutil.JSONOptions{}, svc, &testutil.ConfirmPrompter{Answer: true}, "doc_abc"))

	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `{"id":"doc_abc"`), "expected bare object; got %q", got)
	assert.Contains(t, got, `"deleted":true`)
	assert.NotContains(t, got, `"ok":`)
}

func TestDelete_NotFound_404(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, nil, svc, &testutil.ConfirmPrompter{}, "doc_missing")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 500: internal")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, nil, svc, &testutil.ConfirmPrompter{}, "doc_x")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestDelete_ConfirmYes(t *testing.T) {
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, nil, svc, &testutil.ConfirmPrompter{Answer: true}, "doc_abc")
	require.NoError(t, err)
	assert.Equal(t, 1, svc.calls, "user said yes ⇒ delete proceeds")
	assert.Contains(t, out.String(), "✓")
}

func TestDelete_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, nil, svc, &testutil.ConfirmPrompter{Answer: false}, "doc_abc")
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
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, nil, svc, &testutil.ConfirmPrompter{Err: errors.New("no tty")}, "doc_abc")
	require.Error(t, err)
	assert.Equal(t, 0, svc.calls)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
}

// TestDelete_NoYes_NonTTY_RequiresConfirmation: when stdout isn't a TTY
// (typical agent pipe / CI), the destructive-write protocol requires
// explicit -y/--yes. The CLI exits 10 with input.confirmation_required,
// never silently proceeds. See cli/AGENTS.md "Exit codes".
func TestDelete_NoYes_NonTTY_RequiresConfirmation(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, nil, svc, &testutil.ConfirmPrompter{Err: errors.New("no tty")}, "doc_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 0, svc.calls, "non-TTY without -y must not call DeleteKnowledge")
	assert.Equal(t, 10, cmdutil.ExitCode(err))
}
