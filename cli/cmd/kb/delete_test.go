package kb

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// fakeDeleteSvc records what id was deleted.
type fakeDeleteSvc struct {
	err    error
	gotID  string
	called bool
}

func (f *fakeDeleteSvc) DeleteKnowledgeBase(_ context.Context, id string) error {
	f.called = true
	f.gotID = id
	return f.err
}

func TestDelete_Success_WithForce(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	opts := &DeleteOptions{Yes: true}
	require.NoError(t, runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, p, "kb_force"))

	assert.True(t, svc.called)
	assert.Equal(t, "kb_force", svc.gotID)
	assert.False(t, p.Asked, "--force must skip the confirm prompt")
	assert.Contains(t, out.String(), "✓ Deleted")
	assert.Contains(t, out.String(), "kb_force")
}

func TestDelete_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	p := &testutil.ConfirmPrompter{}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, p, "kb_missing")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	// SetForTest uses bytes.Buffer for Out - IsStdoutTTY() = false. Without
	// -y/--yes, exit-10 protocol fires (see cli/README.md): the CLI must NOT
	// silently proceed in scripted contexts.
	iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	err := runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, p, "kb_nontty")

	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.False(t, svc.called, "non-TTY without -y must not call DeleteKnowledgeBase")
	assert.False(t, p.Asked, "non-TTY ⇒ Confirm is never invoked")
	assert.Equal(t, 10, cmdutil.ExitCode(err), "exit code 10 per destructive-write protocol")
}

func TestDelete_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	opts := &DeleteOptions{Yes: true}
	require.NoError(t, runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, p, "kb_json"))

	got := out.String()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &env), "expected valid JSON envelope, got %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.Equal(t, "kb_json", env.Data["id"], "envelope.data.id must be kb_json")
	assert.Equal(t, true, env.Data["deleted"], "envelope.data.deleted must be true")
}

// The remaining tests cover the interactive confirm path which only fires
// under IsStdoutTTY() && !JSONOut - exercised via SetForTestWithTTY.

func TestDelete_ConfirmYes(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, p, "kb_yes"))

	assert.True(t, p.Asked, "confirm prompt should fire on TTY without --force")
	assert.True(t, svc.called, "answer=yes ⇒ delete proceeds")
	assert.Equal(t, "kb_yes", svc.gotID)
}

func TestDelete_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, p, "kb_no")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.True(t, p.Asked)
	assert.False(t, svc.called, "answer=no ⇒ delete must NOT run")
	assert.Contains(t, errBuf.String(), "Aborted")
}

func TestDelete_ConfirmPrompterError(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Err: prompt.ErrAgentNoPrompt}
	err := runDelete(context.Background(), &DeleteOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, p, "kb_err")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code,
		"prompter error should surface as missing-flag (pass --force)")
	assert.False(t, svc.called)
}

func TestDelete_JSONOut_NoYes_RequiresConfirmation(t *testing.T) {
	// Even on a TTY, --format json indicates a scripted caller; cannot prompt.
	// Exit-10 protocol must fire when -y is absent.
	iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	opts := &DeleteOptions{}
	err := runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, p, "kb_jtty")

	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.False(t, p.Asked, "--format json must skip the prompt even on TTY")
	assert.False(t, svc.called, "--format json without -y must not call DeleteKnowledgeBase")
	assert.Equal(t, 10, cmdutil.ExitCode(err))
}

func TestDelete_JSONOut_WithYes_Proceeds(t *testing.T) {
	// --format json + -y is the agent happy-path: scripted caller with explicit
	// approval. Must call SDK and emit the bare result object.
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	opts := &DeleteOptions{Yes: true}
	require.NoError(t, runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, p, "kb_jtty"))

	assert.False(t, p.Asked, "-y must skip the prompt")
	assert.True(t, svc.called)
	assert.Contains(t, out.String(), `"deleted":true`)
}

func TestKbDelete_NoYes_JSONMode_AttachesRiskAndRetry(t *testing.T) {
	// Non-TTY + JSON mode without -y must return CodeInputConfirmationRequired
	// with risk.action == "kb.delete" and retry_command == "weknora kb delete kb_x -y".
	// Regression test for H1: ConfirmDestructive must attach risk + retry_command.
	iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	opts := &DeleteOptions{Yes: false}

	err := runDelete(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, p, "kb_x")

	require.Error(t, err, "expected confirmation_required error")
	ce := cmdutil.AsError(err)
	require.NotNil(t, ce, "expected *cmdutil.Error; got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, ce.Code)
	assert.False(t, svc.called, "SDK must not be called without confirmation")
	assert.False(t, p.Asked, "--format json must not prompt even on test setup")
	require.NotNil(t, ce.Risk, "expected risk metadata on confirmation_required error")
	assert.Equal(t, "kb.delete", ce.Risk.Action, "expected risk.action == kb.delete")
	assert.Equal(t, "destructive", ce.Risk.Level, "expected risk.level == destructive")
	assert.Equal(t, "weknora kb delete kb_x -y", ce.RetryCommand, "expected retry_command")
}
