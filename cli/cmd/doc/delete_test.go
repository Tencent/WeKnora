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

// scriptedConfirm satisfies prompt.Prompter and returns predetermined answers.
type scriptedConfirm struct{ confirmReturn bool }

func (s scriptedConfirm) Input(string, string) (string, error) { return "", nil }
func (s scriptedConfirm) Password(string) (string, error)      { return "", nil }
func (s scriptedConfirm) Confirm(string, bool) (bool, error)   { return s.confirmReturn, nil }

// errPrompter returns an error from Confirm — simulates a non-TTY agent
// prompter.
type errPrompter struct{}

func (errPrompter) Input(string, string) (string, error) { return "", nil }
func (errPrompter) Password(string) (string, error)      { return "", nil }
func (errPrompter) Confirm(string, bool) (bool, error) {
	return false, errors.New("no tty")
}

func TestDelete_Success_WithForce(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{Yes: true}
	// Force=true short-circuits the confirm path; the prompter must not be
	// consulted, so any value works.
	require.NoError(t, runDelete(context.Background(), opts, svc, scriptedConfirm{confirmReturn: false}, "doc_abc"))

	assert.Equal(t, "doc_abc", svc.got)
	assert.Equal(t, 1, svc.calls)
	assert.Contains(t, out.String(), "✓")
	assert.Contains(t, out.String(), "doc_abc")
}

func TestDelete_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{Yes: true, JSONOut: true}
	require.NoError(t, runDelete(context.Background(), opts, svc, scriptedConfirm{confirmReturn: true}, "doc_abc"))

	got := out.String()
	assert.True(t, strings.HasPrefix(got, `{"ok":true`), "envelope should start with ok:true; got %q", got)
	assert.Contains(t, got, `"id":"doc_abc"`)
	assert.Contains(t, got, `"deleted":true`)
}

func TestDelete_NotFound_404(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, svc, scriptedConfirm{}, "doc_missing")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 500: internal")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, svc, scriptedConfirm{}, "doc_x")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestDelete_ConfirmYes(t *testing.T) {
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, svc, scriptedConfirm{confirmReturn: true}, "doc_abc")
	require.NoError(t, err)
	assert.Equal(t, 1, svc.calls, "user said yes ⇒ delete proceeds")
	assert.Contains(t, out.String(), "✓")
}

func TestDelete_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, svc, scriptedConfirm{confirmReturn: false}, "doc_abc")
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
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, svc, errPrompter{}, "doc_abc")
	require.Error(t, err)
	assert.Equal(t, 0, svc.calls)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
}

// TestDelete_NoForce_NonTTY_Proceeds: when stdout isn't a TTY (typical agent
// pipe / CI), the confirm guard is skipped. This documents the existing
// contract — destructive ops in a pipe rely on the caller having chosen to
// pipe (intent expressed by the redirection) and on agents passing --force
// explicitly. Mirrors `weknora kb delete`.
func TestDelete_NoForce_NonTTY_Proceeds(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: false}, svc, errPrompter{}, "doc_abc")
	require.NoError(t, err, "non-TTY non-json invocation should proceed without prompting")
	assert.Equal(t, 1, svc.calls)
}
