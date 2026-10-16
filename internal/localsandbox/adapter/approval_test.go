package adapter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

type recordingCommandApprover struct {
	decision sandbox.CommandApprovalDecision
	requests []sandbox.CommandApprovalRequest
}

func (r *recordingCommandApprover) ApproveCommand(
	_ context.Context, req sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	r.requests = append(r.requests, req)
	return r.decision, nil
}

func TestExecShellCommandAsksThroughContextApprover(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	approver := &recordingCommandApprover{decision: sandbox.CommandApprovalDecision{Approved: true}}
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{
		Approver: approver, Command: "rm notes.md",
	})

	res, err := a.ExecShellCommand(ctx, "s1", "wrapped", "", 0, nil)
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Len(t, approver.requests, 1)
	require.Equal(t, "delete", approver.requests[0].Reason)
	require.Equal(t, "rm notes.md", approver.requests[0].Command)
}

func TestExecShellCommandRefusesDeleteWithoutApprover(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := sandbox.WithCommandApproval(context.Background(), sandbox.CommandApproval{Command: "rm notes.md"})

	res, err := a.ExecShellCommand(ctx, "s1", "wrapped", "", 0, nil)
	require.NoError(t, err)
	require.Equal(t, 1, res.ExitCode)
	require.Contains(t, res.Stderr, "needs the user's approval")
}

func TestApproverBridgeNarrowsOnlyTheProposedPath(t *testing.T) {
	inner := &recordingCommandApprover{decision: sandbox.CommandApprovalDecision{
		Approved: true, Session: true, Access: "read",
	}}
	bridge := approverBridge{inner: inner}
	proposed := localsandbox.Grant{Path: "/Users/dev/other", Access: localsandbox.AccessWrite}

	d, err := bridge.Approve(context.Background(), localsandbox.ApprovalRequest{
		SessionID: "s1", Command: "touch x", Reason: localsandbox.ReasonDenied,
		Proposed: &proposed, FirstAttemptRan: true, AllowSession: true,
	})
	require.NoError(t, err)
	require.True(t, d.Approved)
	require.True(t, d.Session)
	require.Equal(t, &localsandbox.Grant{Path: proposed.Path, Access: localsandbox.AccessRead}, d.Grant)
	req := inner.requests[0]
	require.Equal(t, "sandbox_denied", req.Reason)
	require.Equal(t, proposed.Path, req.GrantPath)
	require.Equal(t, "write", req.GrantAccess)
	require.True(t, req.FirstAttemptRan)
}

func TestApproverBridgePassesSessionRules(t *testing.T) {
	inner := &recordingCommandApprover{decision: sandbox.CommandApprovalDecision{Approved: true}}
	bridge := approverBridge{inner: inner}

	_, err := bridge.Approve(context.Background(), localsandbox.ApprovalRequest{
		SessionID: "s1", Command: "rm -rf build", Reason: localsandbox.ReasonDelete,
		AllowSession: true, SessionRules: []string{"rm -f -r"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"rm -f -r"}, inner.requests[0].SessionRules)
}

func TestLayoutForAddsSessionGrants(t *testing.T) {
	l := LayoutFor(localsandbox.Workspace{Root: "/w"},
		localsandbox.Grant{Path: "/rw", Access: localsandbox.AccessWrite},
		localsandbox.Grant{Path: "/ro", Access: localsandbox.AccessRead},
	)
	require.Equal(t, []string{"/w", "/rw"}, l.WriteRoots)
	require.Equal(t, []string{"/w", "/rw", "/ro"}, l.ReadRoots)
}

func TestAdapterReleasesSessionState(t *testing.T) {
	var a any = New(nil)
	releaser, ok := a.(sandbox.SessionStateReleaser)
	require.True(t, ok)
	releaser.ReleaseSessionState(context.Background(), "s1")
}
