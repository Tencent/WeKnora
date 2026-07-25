package messagecmd

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

type fakeDeleteSvc struct {
	err       error
	gotSessID string
	gotMsgID  string
}

func (s *fakeDeleteSvc) DeleteMessage(_ context.Context, sessionID, messageID string) error {
	s.gotSessID, s.gotMsgID = sessionID, messageID
	return s.err
}

// TestRunDelete_YesSkipsPromptAndDeletes verifies that -y bypasses the confirm
// prompt, calls the service with the correct IDs, and emits {"deleted":true}
// JSON.
func TestRunDelete_YesSkipsPromptAndDeletes(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{SessionID: "s1", MessageID: "m1", Yes: true}
	require.NoError(t, runDelete(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{}))
	assert.Equal(t, "s1", svc.gotSessID)
	assert.Equal(t, "m1", svc.gotMsgID)
	assert.Contains(t, out.String(), `"deleted":true`)
}

// TestRunDelete_NoYesInJSONModeIsConfirmationRequired verifies that without -y
// on a non-TTY (JSON mode), the CLI returns exit 10 /
// input.confirmation_required and does NOT call the service.
func TestRunDelete_NoYesInJSONModeIsConfirmationRequired(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	opts := &DeleteOptions{SessionID: "s1", MessageID: "m1", Yes: false}
	err := runDelete(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err), "exit 10 per destructive-write protocol")
	assert.Empty(t, svc.gotMsgID, "SDK must not be called without confirmation")
}

// TestRunDelete_TTY_ConfirmYes_Deletes verifies the interactive TTY path where
// the user answers yes: service is called, text output contains success line.
func TestRunDelete_TTY_ConfirmYes_Deletes(t *testing.T) {
	out, _ := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	opts := &DeleteOptions{SessionID: "s1", MessageID: "m1"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	require.NoError(t, runDelete(context.Background(), opts, fopts, svc, p))
	assert.True(t, p.Asked)
	assert.Equal(t, "m1", svc.gotMsgID)
	assert.Equal(t, "s1", svc.gotSessID)
	assert.Contains(t, out.String(), "Deleted message m1")
}

// TestRunDelete_TTY_ConfirmNo_Aborts verifies the interactive TTY path where
// the user declines: service is NOT called, error has CodeUserAborted.
func TestRunDelete_TTY_ConfirmNo_Aborts(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	opts := &DeleteOptions{SessionID: "s1", MessageID: "m1"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	err := runDelete(context.Background(), opts, fopts, svc, p)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Empty(t, svc.gotMsgID, "answer=no must not call DeleteMessage")
	assert.Contains(t, errBuf.String(), "Aborted")
}

// TestRunDelete_ServiceError_Propagates verifies that an SDK-level error is
// wrapped and returned (service called but returns HTTP error).
func TestRunDelete_ServiceError_Propagates(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	opts := &DeleteOptions{SessionID: "s1", MessageID: "missing", Yes: true}
	err := runDelete(context.Background(), opts, jsonOpts(), svc, &testutil.ConfirmPrompter{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}
