package kb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// fakeDeleteSvc records what id was deleted.
type fakeDeleteSvc struct {
	err     error
	gotID   string
	called  bool
}

func (f *fakeDeleteSvc) DeleteKnowledgeBase(_ context.Context, id string) error {
	f.called = true
	f.gotID = id
	return f.err
}

// confirmPrompter scripts a Confirm answer; Input/Password are unused here.
type confirmPrompter struct {
	answer bool
	err    error
	asked  bool
}

func (c *confirmPrompter) Input(string, string) (string, error) { return "", prompt.ErrAgentNoPrompt }
func (c *confirmPrompter) Password(string) (string, error)      { return "", prompt.ErrAgentNoPrompt }
func (c *confirmPrompter) Confirm(string, bool) (bool, error) {
	c.asked = true
	return c.answer, c.err
}

func TestDelete_Success_WithForce(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{}
	opts := &DeleteOptions{Force: true}
	require.NoError(t, runDelete(context.Background(), opts, svc, p, "kb_force"))

	assert.True(t, svc.called)
	assert.Equal(t, "kb_force", svc.gotID)
	assert.False(t, p.asked, "--force must skip the confirm prompt")
	assert.Contains(t, out.String(), "✓ Deleted")
	assert.Contains(t, out.String(), "kb_force")
}

func TestDelete_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	p := &confirmPrompter{}
	err := runDelete(context.Background(), &DeleteOptions{Force: true}, svc, p, "kb_missing")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_NonTTY_NoPrompt_NoForce(t *testing.T) {
	// SetForTest uses bytes.Buffer for Out — IsStdoutTTY() = false. Confirm
	// path must be skipped entirely, so DeleteKnowledgeBase should run.
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{}, svc, p, "kb_nontty"))

	assert.True(t, svc.called, "non-TTY must skip prompt and proceed")
	assert.False(t, p.asked, "non-TTY ⇒ Confirm is never invoked")
	assert.Contains(t, out.String(), "kb_nontty")
}

func TestDelete_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{}
	opts := &DeleteOptions{Force: true, JSONOut: true}
	require.NoError(t, runDelete(context.Background(), opts, svc, p, "kb_json"))

	got := out.String()
	assert.True(t, strings.HasPrefix(got, `{"ok":true`), "envelope should start with ok:true; got %q", got)
	assert.Contains(t, got, `"id":"kb_json"`)
	assert.Contains(t, got, `"deleted":true`)
	assert.Contains(t, got, `"kb_id":"kb_json"`)
}

// The remaining tests cover the interactive confirm path which only fires
// under IsStdoutTTY() && !JSONOut — exercised via SetForTestWithTTY.

func TestDelete_ConfirmYes(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{answer: true}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{}, svc, p, "kb_yes"))

	assert.True(t, p.asked, "confirm prompt should fire on TTY without --force")
	assert.True(t, svc.called, "answer=yes ⇒ delete proceeds")
	assert.Equal(t, "kb_yes", svc.gotID)
}

func TestDelete_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{answer: false}
	err := runDelete(context.Background(), &DeleteOptions{}, svc, p, "kb_no")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.True(t, p.asked)
	assert.False(t, svc.called, "answer=no ⇒ delete must NOT run")
	assert.Contains(t, errBuf.String(), "Aborted")
}

func TestDelete_ConfirmPrompterError(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{err: prompt.ErrAgentNoPrompt}
	err := runDelete(context.Background(), &DeleteOptions{}, svc, p, "kb_err")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code,
		"prompter error should surface as missing-flag (pass --force)")
	assert.False(t, svc.called)
}

func TestDelete_JSONOut_SkipsPrompt(t *testing.T) {
	// Even on a TTY, --json indicates a scripted caller; don't prompt.
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &confirmPrompter{}
	opts := &DeleteOptions{JSONOut: true}
	require.NoError(t, runDelete(context.Background(), opts, svc, p, "kb_jtty"))

	assert.False(t, p.asked, "--json must skip the prompt even on TTY")
	assert.True(t, svc.called)
	assert.Contains(t, out.String(), `"deleted":true`)
}
