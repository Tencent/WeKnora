package sessioncmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// fakeStopSvc records the (sessionID, messageID) pair passed to StopSession
// and optionally returns a canned error.
type fakeStopSvc struct {
	err       error
	gotSessID string
	gotMsgID  string
}

func (s *fakeStopSvc) StopSession(_ context.Context, sessionID, messageID string) error {
	s.gotSessID = sessionID
	s.gotMsgID = messageID
	return s.err
}

// TestRunStop_CallsSDKAndEmits verifies that runStop passes (sessionID,
// messageID) to the SDK and emits a JSON envelope with stopped:true.
func TestRunStop_CallsSDKAndEmits(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeStopSvc{}
	opts := &StopOptions{SessionID: "s1", MessageID: "m1"}
	require.NoError(t, runStop(context.Background(), opts, jsonOpts(), svc))

	assert.Equal(t, "s1", svc.gotSessID, "SDK must receive the session-id")
	assert.Equal(t, "m1", svc.gotMsgID, "SDK must receive the message-id")
	assert.Contains(t, out.String(), `"stopped":true`, "stdout must contain stopped:true")
}

// TestRunStop_TextMode verifies that runStop writes the human-readable success
// line and calls the SDK when format is text (not JSON).
func TestRunStop_TextMode(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeStopSvc{}
	opts := &StopOptions{SessionID: "sess_xyz", MessageID: "msg_abc"}
	require.NoError(t, runStop(context.Background(), opts, textOpts(), svc))

	assert.Equal(t, "sess_xyz", svc.gotSessID, "SDK must receive the session-id")
	assert.Equal(t, "msg_abc", svc.gotMsgID, "SDK must receive the message-id")
	assert.Contains(t, out.String(), "✓ Stopped generation for message msg_abc")
}

// TestStop_MessageRequired verifies that NewCmdStop refuses to run without
// --message (the flag is marked required by cobra).
func TestStop_MessageRequired(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStop(f)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"sess_xyz"}) // positional only, no --message
	err := cmd.Execute()
	require.Error(t, err, "expected required-flag error when --message is missing")
	assert.True(t,
		strings.Contains(err.Error(), "message") || strings.Contains(err.Error(), "required"),
		"error should mention the required --message flag: %v", err)
}
