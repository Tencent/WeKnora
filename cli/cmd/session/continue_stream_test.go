package sessioncmd

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
	sdk "github.com/Tencent/WeKnora/client"
)

// scriptedContinueStreamSvc serves a canned stream of StreamResponse events
// to runContinueStream and records the (sessionID, messageID) passed in.
type scriptedContinueStreamSvc struct {
	events    []*sdk.StreamResponse
	streamErr error
	got       struct {
		sessionID string
		messageID string
	}
}

func (s *scriptedContinueStreamSvc) ContinueStream(_ context.Context, sessionID, messageID string, cb func(*sdk.StreamResponse) error) error {
	s.got.sessionID = sessionID
	s.got.messageID = messageID
	for _, e := range s.events {
		if err := cb(e); err != nil {
			return err
		}
	}
	return s.streamErr
}

func contStreamAnswer(content string) *sdk.StreamResponse {
	return &sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: content}
}
func contStreamComplete() *sdk.StreamResponse {
	return &sdk.StreamResponse{ResponseType: sdk.ResponseTypeComplete, Done: true}
}

// TestContinueStream_NDJSON_FirstLineIsInitWithMessageID verifies the
// CLI-injected init line carries both session_id and message_id, so agents
// can key dedupe tables on the resumed message before the first SDK frame
// arrives.
func TestContinueStream_NDJSON_FirstLineIsInitWithMessageID(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedContinueStreamSvc{
		events: []*sdk.StreamResponse{contStreamAnswer("hello"), contStreamComplete()},
	}
	opts := &ContinueStreamOptions{SessionID: "sess_xyz", MessageID: "msg_abc"}
	require.NoError(t, runContinueStream(context.Background(), opts, ndjsonOpts(), svc))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 1, "expected at least the init line")

	var first struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		MessageID string `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first), "first line must be valid JSON: %q", lines[0])
	assert.Equal(t, "init", first.Type)
	assert.Equal(t, "sess_xyz", first.SessionID)
	assert.Equal(t, "msg_abc", first.MessageID, "init.message_id must echo --message (anchor for dedupe)")
}

// TestContinueStream_NDJSON_PassthroughEvents verifies: 1 init line + N SDK
// events = N+1 total lines, all valid JSON.
func TestContinueStream_NDJSON_PassthroughEvents(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedContinueStreamSvc{
		events: []*sdk.StreamResponse{
			contStreamAnswer("alpha"),
			contStreamAnswer("beta"),
			contStreamComplete(),
		},
	}
	opts := &ContinueStreamOptions{SessionID: "sess_x", MessageID: "msg_y"}
	require.NoError(t, runContinueStream(context.Background(), opts, ndjsonOpts(), svc))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	// 1 init + 3 SDK events = 4 lines.
	require.Equal(t, 4, len(lines), "expected init + 3 SDK events:\n%s", out.String())
	for i, line := range lines {
		var obj map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &obj), "line %d not valid JSON: %q", i+1, line)
	}
}

// TestContinueStream_PassesSessionAndMessageIDToSDK verifies the args + flag
// flow through to the SDK call.
func TestContinueStream_PassesSessionAndMessageIDToSDK(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedContinueStreamSvc{events: []*sdk.StreamResponse{contStreamComplete()}}
	opts := &ContinueStreamOptions{SessionID: "sess_42", MessageID: "msg_99"}
	require.NoError(t, runContinueStream(context.Background(), opts, ndjsonOpts(), svc))
	assert.Equal(t, "sess_42", svc.got.sessionID)
	assert.Equal(t, "msg_99", svc.got.messageID)
}

// TestContinueStream_EmptySessionID_Rejected guards the direct-test entry
// point (cobra blocks empty positional via ExactArgs(1), but the runtime
// core must also refuse empty strings).
func TestContinueStream_EmptySessionID_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedContinueStreamSvc{}
	opts := &ContinueStreamOptions{SessionID: "", MessageID: "msg_x"}
	err := runContinueStream(context.Background(), opts, ndjsonOpts(), svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

// TestContinueStream_EmptyMessageID_Rejected guards the direct-test entry
// point.
func TestContinueStream_EmptyMessageID_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedContinueStreamSvc{}
	opts := &ContinueStreamOptions{SessionID: "sess_x", MessageID: ""}
	err := runContinueStream(context.Background(), opts, ndjsonOpts(), svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

// TestContinueStream_Cancellation_MapsToOperationCancelled verifies a
// cancelled context maps to operation.cancelled (Ctrl-C lineage).
func TestContinueStream_Cancellation_MapsToOperationCancelled(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := &scriptedContinueStreamSvc{streamErr: context.Canceled}
	opts := &ContinueStreamOptions{SessionID: "sess_x", MessageID: "msg_x"}
	err := runContinueStream(ctx, opts, ndjsonOpts(), svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeOperationCancelled, typed.Code)
}

// TestContinueStream_NotFound_MapsToResourceNotFound verifies an SDK 404
// (e.g. unknown message_id, or buffer expired past TTL) is classified by
// the canonical HTTP classifier.
func TestContinueStream_NotFound_MapsToResourceNotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedContinueStreamSvc{streamErr: errors.New("HTTP error 404: not found")}
	opts := &ContinueStreamOptions{SessionID: "sess_x", MessageID: "msg_missing"}
	err := runContinueStream(context.Background(), opts, ndjsonOpts(), svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

// TestContinueStream_RequiresMessageFlag verifies cobra refuses to run the
// command without --message (the flag is marked required).
func TestContinueStream_RequiresMessageFlag(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdContinueStream(f)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"sess_xyz"}) // positional only, no --message
	err := cmd.Execute()
	require.Error(t, err, "expected required-flag error when --message is missing")
	assert.True(t,
		strings.Contains(err.Error(), "message") || strings.Contains(err.Error(), "required"),
		"error should mention the required --message flag: %v", err)
	// Note: cobra's bare required-flag error here is not yet wrapped as
	// FlagError - that mapping happens in cmd/root.go for top-level Execute.
	// Asserting the message text is sufficient for this unit-level check;
	// exit-code mapping is covered by cmd/root tests.
}

// TestContinueStream_RequiresSessionIDArg verifies cobra refuses to run the
// command without the positional <session-id>.
func TestContinueStream_RequiresSessionIDArg(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdContinueStream(f)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--message", "msg_abc"}) // missing positional
	err := cmd.Execute()
	require.Error(t, err, "expected ExactArgs(1) error when <session-id> is missing")
	// Cobra reports "accepts 1 arg(s), received 0" — assert on substance,
	// not exit-code mapping (the latter happens in cmd/root, see
	// TestContinueStream_RequiresMessageFlag note).
	assert.True(t,
		strings.Contains(err.Error(), "arg") || strings.Contains(err.Error(), "received"),
		"error should mention arg-count: %v", err)
}
